# Putting the site on a server with your own domain

This sets the site up on a server you control, with a free HTTPS certificate for your own domain. You need:

- A Linux server (a small VPS is plenty) with a **public IP address**, and **Docker** with the Compose plugin installed.
- A **domain name** you can edit DNS records for.
- Ports **80** and **443** open to the internet (and 22 for SSH). Nothing else needs to be public.

## 1. Get the code and configure it

```bash
git clone <your repository> webbuilder && cd webbuilder
cp .env.example .env
```

Edit `.env`:

- `POSTGRES_PASSWORD` – set a long random password **before the first start** (the database is created with it).
- `TRUST_PROXY=true` – the site sits behind Caddy.
- `SETUP_TOKEN` – set this to a long random string. The first-run page will then ask for it, so nobody else can
  claim a fresh server before you do. (`openssl rand -hex 24` makes a good one.)

## 2. Start it

```bash
docker compose --profile prod up -d --build --pull always
```

This starts the database, the app and Caddy (the web server that handles HTTPS). The first build takes a few minutes.

Until you add a domain, the site works over plain HTTP at the server's IP address. Open `http://<server-ip>/setup`,
enter your `SETUP_TOKEN`, and create your admin account.

## 3. Point your domain at the server

Where you manage DNS, add:

| Type    | Name              | Value                                  |
|---------|-------------------|----------------------------------------|
| `A`     | `example.com`     | your server's IPv4 address             |
| `AAAA`  | `example.com`     | your server's IPv6 address (if any)    |
| `CNAME` | `www`             | `example.com`                          |

DNS changes can take from a few minutes to a few hours.

## 4. Tell the site its domain

Log in, open **Domain** in the admin, enter `example.com` (no `www`, no `https://`), and save. Then press **Check**.
The check looks up the domain and visits it over HTTP and HTTPS from the server. The first HTTPS visit makes Caddy
request the certificate, so it can take up to a minute; run the check again if it says HTTPS isn't ready.

Once it passes:

- `https://example.com` is your site, with a valid certificate that renews automatically.
- `http://example.com` and `https://www.example.com` redirect to `https://example.com`.
- Cookies are marked Secure on HTTPS requests automatically, and browsers are told to use HTTPS only (HSTS).

## How certificates are kept safe

Caddy is set up for *on-demand* certificates: it requests one the first time a visitor arrives with a new hostname.
To stop anyone pointing a random name at your server and using up Let's Encrypt's rate limits, Caddy first asks the
app whether the name is allowed. The app only says yes for the domain saved in the admin (and its `www.` form), and
answers on an internal-only port (`8081`) that is never published.

## Security checklist

- [ ] `POSTGRES_PASSWORD` changed from the default, and `.env` not committed anywhere.
- [ ] `SETUP_TOKEN` set before the first start.
- [ ] Firewall allows only 22, 80 and 443. (The database is bound to `127.0.0.1` and the app's own port 8080 to
      `127.0.0.1`; Docker bypasses some firewalls such as ufw for published ports, so check with `ss -tlnp`
      or an external port scan that nothing else answers.)
- [ ] Domain check passes, and `https://` works in a browser with no warnings.
- [ ] Every person has their own account; remove accounts that are no longer needed (**Users**).
- [ ] Backups are taken and a restore has been tried at least once (below).
- [ ] You know how to update (below), and do it periodically.

## Backups

Two things hold your data: the database and the uploaded images.

```bash
# database
docker compose exec -T db pg_dump -U webbuilder webbuilder > backup-$(date +%F).sql

# uploads
docker run --rm -v webbuilder_media:/data -v "$PWD":/backup alpine \
  tar czf /backup/media-$(date +%F).tgz -C /data .
```

To restore into a fresh install: start it, then
`docker compose exec -T db psql -U webbuilder webbuilder < backup.sql`, and unpack the media archive back into the
`webbuilder_media` volume. Copy backups off the server.

Also keep the `caddy_data` volume if you want to avoid re-requesting certificates after a rebuild (not essential:
they are re-issued automatically).

## Updating

```bash
git pull
docker compose --profile prod up -d --build --pull always
```

`--pull always` fetches the newest base images, which is how Go and operating-system security fixes reach you.
Database changes are applied automatically when the app starts. The Go version in the `Dockerfile` is pinned;
the README explains how to check whether it needs bumping.

## Troubleshooting

- **The check says the domain doesn't point anywhere** – DNS hasn't propagated yet, or the record is wrong.
  `dig +short example.com` should print your server's IP.
- **HTTPS fails but HTTP works** – wait a minute and check again; also confirm port 443 is open in your firewall and
  your provider's security group. `docker compose logs caddy` shows certificate messages.
- **The check says "that address isn't public"** – the domain points to a private address (like `192.168.…`); it
  must point to the server's public IP.
- **The check can't connect, but the site works in your browser** – some hosts can't reach their own public address
  from inside. The browser test is the one that matters.
- **"The setup token is missing or incorrect"** – it must match `SETUP_TOKEN` in the server's `.env` exactly.
  After several wrong tries the page locks for 15 minutes.
- **Locked out after a mistake** – the site is also reachable at `http://<server-ip>` for any request that isn't for
  your domain, so you can still log in and fix the domain setting.
- **Containers keep restarting** – `docker compose logs app`. The app container's filesystem is read-only on purpose;
  it writes only to the media volume and a temporary folder.

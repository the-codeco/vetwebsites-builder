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

---

# Running the platform

Everything above runs **one website** on its own domain. The platform is the other mode (`PLATFORM=true`): you run one
server, the **builder**, at an address such as `https://builder.example.com`. Each customer logs in there, edits their
website, previews it, and publishes it as static files to their own cPanel hosting. The builder itself handles the three
things a static site can't: search, the contact form and newsletter sign-ups (the published pages send those to it).

Customers' domains are **not** pointed at the builder, so there is nothing to do per customer on this server.

## 1. Configure

Same server, Docker and ports as above, plus one domain for the builder with a DNS `A` record pointing at the server. In `.env`:

```bash
PLATFORM=true
BASE_URL=https://builder.example.com
BUILDER_DOMAIN=builder.example.com
CADDYFILE=./Caddyfile.platform
TRUST_PROXY=true
SECRET_KEY=<openssl rand -hex 32>
SETUP_TOKEN=<openssl rand -hex 24>
POSTGRES_PASSWORD=<long random string>
```

- `SECRET_KEY` encrypts the hosting passwords customers save. **Keep a copy with your database backups and never change
  it**: without it every saved hosting password is unreadable and customers must enter theirs again.
- `BASE_URL` is baked into emailed links (newsletter confirm/unsubscribe, invitations) and into every published page's
  forms, so settle it before customers publish. Changing it later means every customer must publish again.
- Set the `SMTP_*` variables so the builder can send invitations, password resets and newsletters.
- `QUOTA_*` set what each website may hold by default; a platform administrator can change any website's limits.

## 2. Start it

```bash
docker compose --profile prod up -d --build --pull always
```

Open `https://builder.example.com/setup`, enter the `SETUP_TOKEN` and create the first account: it is the **platform
administrator**. From **Websites** they add a customer (an email address and a website name); the customer gets an
invitation by email, chooses a password and lands in their own website's admin.

All platform data lives in the `platform` volume (every website's pictures, backups and builds) next to the database.
The container's filesystem stays read-only.

## 3. Backups

Back up three things, and copy them off the server:

```bash
# the database (every website lives in it, in its own schema)
docker compose exec -T db pg_dump -U webbuilder webbuilder > backup-$(date +%F).sql

# pictures, backups and builds
docker run --rm -v webbuilder_platform:/data -v "$PWD":/backup alpine \
  tar czf /backup/platform-$(date +%F).tgz -C /data .
```

…and your `SECRET_KEY`. The builder also keeps a rolling backup of each website in `platform/backups/<id>` that a
customer can download from **Backup**.

## 4. Moving an existing single website onto the platform

See "Moving a single website onto the platform" in the README. Stop the old program, take the `pg_dump` above, set the
variables in this section, then run the `-adopt-single-site` command once.

## 5. What the builder needs to reach

- **Customers' hosting**: outbound FTPS (port 21) or SFTP (port 22). If your firewall filters outbound traffic, allow
  those. The builder refuses private-network addresses unless `ALLOW_PRIVATE_HOSTS=true`, which you should leave off.
- **The other direction**: published sites call `https://builder.example.com/f/<token>/…` from visitors' browsers.
  If the builder is down, those pages still load; only search, the contact form and sign-ups fail until it is back.

## Telling a customer how to publish (cPanel)

Give each customer these steps. They only need their hosting details.

1. In cPanel, open **FTP Accounts** and create an account whose directory is the website's folder (usually
   `public_html` for the main domain). Note the FTP server name, username and password. Choose **SFTP** if the host
   offers SSH; otherwise **FTP with encryption (FTPS)**, which works on almost every cPanel host. Plain FTP is not offered.
2. In the builder, open **Your hosting** and fill in the server address, port (21 for FTPS, 22 for SFTP), username,
   password and the folder on the hosting (`public_html`, or `public_html/shop` to put the site in a folder).
3. Enter **Your website's address**, where visitors will find it (for example `https://www.example.com`). The forms and
   search on the published site accept requests only from that address.
4. Press **Save**, then **Test the connection**. If the test complains about the security certificate, enter the name
   the hosting presents in **Certificate name** (FTPS only).
5. Open **Publish** and press **Preview before publishing** to look at the built site, then **Publish all changes**.
   Only changed files are sent, and only files the builder put there earlier are ever removed, so other things in that
   folder are left alone.
6. Optionally tick **Publish by itself when scheduled pages and posts go live** on the hosting screen.

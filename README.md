# Web Builder

A self-hosted website builder and blog, written in Go with PostgreSQL. You log in, build pages out of
blocks, write posts, upload images, restyle the whole site, and put it on your own domain with automatic HTTPS.

- **A proper admin** — a sidebar with icons and grouped sections (it collapses to icons on desktop and becomes a
  drawer on phones), a dashboard with live counts, recent work and shortcuts, light and dark colour schemes that
  follow your system, and keyboard-friendly controls. Editors only see what they may use.
- **Pages from blocks** — hero, text, image, gallery, columns, button, latest posts, spacer, plus pricing tables, testimonials,
  FAQ accordions, tabs, video, maps and layouts that hold other blocks (below). Edit in a form
  with a live preview that updates as you type (before you save); reorder, restyle each block (colours, padding, alignment, width).
- **Menus** — two sections: a top bar and the main navigation (footer links are part of the footer builder). Items can be links, simple
  dropdowns or **mega menus** (up to 6 sections, each a title — which can be a link — over links with short descriptions);
  "Find…" searches your pages and posts to fill in a label and address. Each can open in
  a new tab or be hidden without deleting it. Any section can be **frozen** so it stays on screen while visitors
  scroll (a frozen top bar and header stack neatly). Keyboard-accessible, with a hamburger menu on phones, and
  usable even without JavaScript.
- **SEO** — every page and post can set its own search title, description, sharing image and "don't list this";
  a live preview shows how it may look in search results. Pages get a canonical link, Open Graph and Twitter
  card tags and schema.org data, and the site serves `/sitemap.xml` and `/robots.txt` automatically. An admin
  screen sets site-wide defaults, can hide the whole site while it's being built, and lists pages that need work.
- **Pricing, testimonials, FAQ, tabs, video and maps** — more blocks for the page editor. Pricing shows up to 3 plans (one can be
  highlighted); testimonials up to 3 quotes with optional photos; the FAQ is an accordion of up to 10 questions (it works without
  JavaScript); tabs holds up to 5 panels (without JavaScript they are shown one under another). Video takes a YouTube or Vimeo link
  (shown through the privacy-friendly youtube-nocookie.com / Vimeo "do not track" players);
  the map takes "latitude, longitude" and shows OpenStreetMap. Only those three sites can be embedded: the public pages'
  Content-Security-Policy lists them under `frame-src`, and any other address shows nothing. Note that visitors' browsers contact
  those sites when a page with a video or map loads, so mention them in your privacy policy.
- **Hero** — a big heading with a button, with the picture either behind the text or beside it (left or right; stacks on phones).
- **Footer builder** — Admin → Footer (admins): rows of 1–4 columns (equal, or one wider; left, centred or right), each column a stack of widgets — text (basic HTML), a list or row of links (with "Find…" for your pages and posts), contact details and social links from Site details, your logo with a tagline, a newsletter signup — above an editable bottom line (`{year}` and `{title}` are filled in). The footer can have its own background and text colour and padding. A footer from before the builder, the old footer menu and "show contact details in the footer" are moved into it automatically. Included in backups (format version 4; older versions still restore and are upgraded the same way).
- **Maps** — a Map block shows either OpenStreetMap by latitude and longitude (private, no cookies) or Google Maps found by street address (Google may set its own cookies in the visitor's browser). Public pages allow framing only www.openstreetmap.org and www.google.com for maps, plus the video hosts.
- **Password reset** — "Forgot your password?" on the login page emails a one-time link (valid for an hour) when the site has mail settings and a saved domain; the link is always built from the saved domain, never from the request. An admin can also make a one-time link for anyone under Users → Reset password, which works without email. A reset signs the user out everywhere else and sends a "your password was changed" notice.
- **Scheduled publishing** — a draft page or post can be given a "Publish automatically on" time (set in your own time zone); it goes live by itself within about 20 seconds of that time, even after downtime.
- **Page history** — every change to a page's blocks first saves a restore point (once per person per 10 minutes, the latest 30 kept); the History card on the page editor goes back to any of them, saving the current version first so a restore can itself be undone. History is working data and is not part of backups.
- **Redirects** — Admin → Redirects sends old addresses to new ones (permanent or temporary); changing a page or post's address adds one automatically. Unknown addresses of any depth show the themed not-found page. Included in backups.
- **Phone and WhatsApp links** — `tel:` links work in buttons, menus and cards (`https://wa.me/…` links already worked).
- **Logo, tab icon and menu buttons** — Appearance → Logo and tab icon puts a logo in the header (with or without the title); any plain menu link can be drawn as a button ("Book now").
- **Site details** — Admin → Site details holds phone, WhatsApp, email, address, opening hours and social links once; a "Contact details" block or a footer widget shows them everywhere.
- **Your own fonts** — upload WOFF2 files under Appearance (up to 12, 600 KB each); they appear in the font lists, are served from your own site, and are part of backups.
- **Analytics services** — Admin → Analytics lets an admin enter a Google Analytics 4 ID and/or a Plausible domain. Only the ID is entered, never a script; the public pages' security policy lets through exactly those services, they are not loaded for logged-in users, and by default they wait for the visitor to accept the cookie notice.
- **Shared blocks** — Admin → Shared blocks: write a block once (text, button, form, anything but a layout), add it to as many pages and layout columns as you like, and edit it in one place. Any block on a page can be turned into a shared one with "Save as shared block". A shared block that is still used can't be deleted. Included in backups (format version 3; versions 1 and 2 still restore).
- **Repeating lists** — feature cards, team, testimonials, FAQ and tabs hold as many items as you like (up to 200): "Add" a card, move it up or down, remove it. In code, give a block a `KindRepeater` field with `Item` fields; old pages saved with numbered fields (q1_question…) are upgraded automatically.
- **Feature cards** — cards (icon or picture, title, text, optional link) in a 2, 3 or 4 per row grid, boxed or plain.
- **Team** — people (photo, name, role, short bio, optional link) in a 2, 3 or 4 per row grid, round or square photos.
- **Layout (columns that hold other blocks)** — add a *Layout* block, choose 2, 3 or 4 columns (equal, or with one
  wider), the space between them, how they line up (top, middle, bottom, or all as tall as the tallest) and whether
  the last column comes first on phones. Then add any other block into any column: text beside a form, an image beside
  a map, a column of buttons. Blocks inside a column can be reordered, moved to the next column with ← →, styled
  (colour, padding, alignment) and deleted like any other. Fewer columns later never loses a block: those of the
  columns that went away join the end of the last one. Rules: a layout can't go inside a layout, a column holds up to
  20 blocks, and the 200-blocks-per-page limit counts nested blocks. Phones show the columns one under another.
  Words inside columns are searchable, and layouts are part of backups (backup format version 2; version 1 files
  still restore).
- **Cookie notice** — an optional banner (Appearance → Cookie notice). The public site sets no tracking cookies, so none is
  required by the site itself; turn it on if your visitors or local rules expect one. Choose a plain notice ("Got it") or
  Accept / Decline with a "Cookie settings" link in the footer. The answer is kept in the visitor's browser only, and is
  published as `<html data-consent="accepted|declined">` plus a `wb:consent` event for features that need it.
- **Dark mode** — Default and Journal come with a dark palette. In Appearance choose Off, Switch (a sun/moon button in
  the header, light by default) or Automatic (follows each visitor's device setting, and they can still switch). The
  choice is remembered in the visitor's own browser: no cookie, nothing sent to the server. A Primary colour you set
  is kept in dark mode unless you give it a dark colour. The **Dark mode colours** card in Appearance lets you recolour the
  dark palette (background, surface, text, muted, primary, text on primary) with a live preview shown in dark mode; it warns
  when a pair of colours is hard to read (WCAG contrast) but never blocks you, and each theme keeps its own dark colours.
- **Search** — a search box in the header (turn it off in Appearance) and a results page at `/search`. It searches
  page text (every text block) and posts, understands word endings ("tomato" finds "tomatoes"), ranks title matches
  first, highlights the matching words, and leaves out drafts and anything marked "don't list".
- **Contact form** — a block you drop on any page. Messages land in an admin inbox (with an unread badge) and can
  also be emailed to you. Spam is handled without CAPTCHAs: a hidden trap field, a form that must have been open
  for a few seconds, and hourly limits. Nothing about the sender is kept except what they typed. With email set up you
  can **reply from the admin**: the answer is sent from the site's address with the visitor's message quoted, your own
  address as Reply-To, and a copy kept with the message (30 replies per person per hour at most).
- **Newsletter signup** — a block that collects email addresses. With email set up and a custom domain saved, new
  addresses must be confirmed by a link (double opt-in); otherwise they count at once. Admins see the list, export
  it as CSV and remove people.
- **Newsletters (sent mail)** — Admin → Newsletters. Write a plain-text newsletter (saved as a draft), send yourself a test,
  then send it to everyone confirmed on the list. It needs email set up (`SMTP_*`) and a saved custom domain (every
  email carries that person's own unsubscribe link, plus `List-Unsubscribe` one-click headers). Sending is a queue in
  the database worked through one message at a time (about two a second): it survives restarts, retries temporary
  failures (up to 3 attempts), gives up at once on addresses the server refuses, skips anyone who unsubscribes in the
  meantime, and can be stopped part-way. The number of people you are shown is the number you confirm, so a list that
  changed in between is noticed. Pending and failed messages are shown per newsletter. Admin-only.
- **Backup and restore** — Admin → Backup (admin-only). *Download a backup*: one .zip with all pages and blocks, posts,
  categories, menus, theme/colours/dark colours/custom CSS/SEO/cookie-notice settings and every uploaded image, each
  file checksummed. Optionally the subscriber list and contact messages too (personal data: off by default). **Never**
  included: user accounts and sessions (their password hashes would travel with the file), the domain, email settings,
  analytics and sent newsletters. The server also keeps its own copies in `BACKUP_DIR`: a scheduled backup about once a
  day (newest `BACKUP_KEEP`, default 7; `0` turns it off), manual ones kept until deleted, and a *pre-restore* copy
  made just before every restore. *Restore* (from an uploaded file or a saved one) first shows what is in the file and
  what would be replaced, needs you to type `RESTORE`, saves the current site first so a mistake can be undone, and
  then replaces pages, posts, categories, menus, images and settings in one database transaction (all or nothing).
  A file being restored is never trusted: only the expected file names, sizes capped, SHA-256 of every file checked,
  every value cleaned like the editor cleans typed text (unknown blocks, unsafe links, scripts, bad slugs, invalid
  menus and theme settings are dropped and reported), and every image decoded. The backup folder is separate from the
  public media folder. Backups held on the same server protect against mistakes, not against losing the server:
  download copies and keep them elsewhere. For a copy of the *whole* database (including accounts) use
  `docker compose exec db pg_dump -U webbuilder webbuilder > webbuilder.sql`.
- **Analytics** — built-in, cookieless page-view statistics (no scripts, no IP addresses stored, no consent banner needed).
- **Blog** — posts with categories and a cover image, a formatting toolbar, paginated listings, an RSS feed.
- **Media library** — uploads are checked by content (not file name), thumbnails are generated, images can be
  picked from any image field. Imaging tools:
  - **Privacy**: JPEG uploads lose their hidden details (EXIF/XMP/IPTC: GPS location, camera, time, owner name) without
    being re-encoded; the colour profile is kept. A photo whose EXIF says "rotate me" is turned for real first, so
    thumbnails and every browser show it upright.
  - **Responsive images**: the site's own images are published with `srcset`/`sizes` (image, gallery, hero, blog covers),
    so phones fetch 480 or 960 px wide copies instead of the full file. Copies are made the first time they are asked for
    (`/media/…jpg?w=960`; only 480, 960 and 1600 are allowed; never enlarged), kept on disk, and deleted with the image.
  - **Editor** (Media → Edit → "Crop, turn or shrink…"): turn, mirror, crop (drag a box, or type pixel numbers; free shape
    or 1:1, 16:9, 3:2, 4:3, 3:4) and shrink to a maximum width. The result is saved as a **new** image with the same alt
    text; the original and the pages using it are not changed. GIFs (which may be animated) can't be edited, and WebP
    images are saved as PNG.
- **Themes** — three included (Default, Journal, Midnight). Change colours, fonts and spacing site-wide with a
  live preview, add custom CSS, preview a theme before activating it.
- **Your own domain** — save a domain in the admin, point DNS at the server, and Caddy gets and renews the
  certificate. A built-in check tells you what's still missing.
- **Accounts** — admins and editors, invitations by one-time link, password changes, user removal.
- One Go binary, server-rendered HTML with a little htmx; no JavaScript build step.

## Quick start

You need Docker (with Compose). From this folder:

```bash
docker compose up -d --build
```

Open <http://localhost:8080/setup> and create the admin account. The site is at <http://localhost:8080/>
and the admin at <http://localhost:8080/admin/>.

To run on a real server with a domain name, see **[docs/DEPLOY.md](docs/DEPLOY.md)**.

## Configuration

Set these in a `.env` file next to `docker-compose.yml` (copy `.env.example`), or as environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | (required; set by compose) | PostgreSQL connection string. |
| `ADDR` | `:8080` | Address the site listens on. |
| `INTERNAL_ADDR` | empty (compose: `:8081`) | A second listener for Caddy's certificate check. Never publish this port. |
| `MEDIA_DIR` | `./media` | Where uploaded images are stored. |
| `TRUST_PROXY` | `false` | `true` behind Caddy: use its `X-Forwarded-*` headers (visitor IP, HTTPS). |
| `SECURE_COOKIES` | `false` | Force the Secure cookie flag even on plain HTTP. Cookies are already Secure on HTTPS requests. |
| `SETUP_TOKEN` | empty | If set, required to create the first admin account. |
| `BACKUP_DIR`, `BACKUP_KEEP`, `BACKUP_PERSONAL` | `./backups`, `7`, `false` | Where saved backups are kept (a different folder from the public media folder; a Docker volume in the compose file), how many scheduled daily backups to keep (`0` = off), and whether scheduled backups include subscribers and messages. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM` | empty (port `587`) | Optional. Lets the site send mail: copies of contact messages to you, replies to visitors, newsletter confirmations and newsletters (STARTTLS is used when the server offers it). Without them, messages only appear in the admin inbox and nothing is sent. |
| `POSTGRES_PASSWORD` | `webbuilder` | Database password. **Change it on a server.** |

## How it's organised

```
cmd/server/          main(): config, database, HTTP servers, graceful shutdown, -healthcheck
internal/app/        wires everything together (also used by the integration tests)
internal/auth/       passwords (argon2id), sessions, CSRF, rate limiting, accounts
internal/admin/      the admin screens (pages, posts, media, menus, appearance, domain, users)
internal/site/       the public site: pages, blog, RSS, theme assets, media serving
internal/blocks/     block types: each declares a schema used for validation AND the editor form
internal/themes/     theme loading and inheritance, templates, CSS tokens
internal/content/    data layer for pages, blocks, posts, categories, menus, settings
internal/media/      upload validation and cleaning, storage, thumbnails, responsive copies, the image editor
internal/search/      site search: full-text queries and snippets
internal/newsletter/  signup list: double opt-in, unsubscribe links, export
internal/campaign/    newsletters: drafts, the send queue and its worker, retries
internal/backup/      backup files: export, checking and restoring one safely, the saved-backups folder, scheduling
internal/formguard/  spam defences shared by public forms (timing token, hourly limits)
internal/mailer/     sending email over SMTP and checking addresses
internal/contact/    contact form: validation, spam defences, inbox storage, email
internal/analytics/  cookieless page-view counting and reports
internal/seo/        sitemap and robots.txt text, absolute URLs, the SEO audit rules
internal/menus/      navigation model and validation: sections, links, dropdowns, mega menus
internal/domain/     custom domain: validation, certificate gate, redirects, connection check
internal/secure/     security headers, body limits, panic recovery
themes/<name>/       a theme: theme.json, theme.css, optional layout.html / blocks/ / blog/ overrides
web/static/          admin CSS and JavaScript (htmx is vendored here)
migrations/          SQL migrations, applied automatically at startup
```

**Adding a block type:** register it in `internal/blocks/types.go` (a block that holds other blocks, like Layout, also gets its children from the renderer: see `RenderBlockTree`) (name, label, fields) and add
`themes/default/blocks/<name>.html`. The editor form, validation and rendering follow from that.

**Menus in themes:** a theme never renders menu items itself. Its `layout.html` places three partials from
`themes/default/partials/menus.html` — `{{template "menu-topbar" .}}`, `{{template "menu-main" .}}` (inside the
header, which should carry `{{if .Nav.Main.Sticky}} is-sticky{{end}}`) and `{{template "menu-footer" .}}` — and
loads the menu script with `<script src="{{.Site.MenuJS}}" defer></script>`. Markup, keyboard behaviour and the mobile menu come from the partials, so
a theme only styles them (see the `.menu-*` rules in the default `theme.css`).

**Footer in themes:** a layout draws the footer as
`<footer class="site-footer{{.Site.Footer.Classes}}" id="site-footer"{{with .Site.Footer.Style}} style="{{.}}"{{end}}>`
holding `{{template "footer-columns" .}}` (the owner's rows of widgets, from `partials/footer.html`) and the bottom line
`{{.Site.Footer.Bottom}}`. The owner's colours arrive as `--footer-bg` and `--footer-fg`; see the `.footer-*` rules in the default `theme.css`.

**Cookie notice in themes:** layouts add `{{template "consent-banner" .}}` just before `</body>` and `{{template "consent-reopen" .}}` in the
footer (both print nothing when the notice is off). The banner starts `hidden` and its script shows it only to visitors who haven't answered.

**Dark mode in themes:** give colour tokens a `"dark"` value in `theme.json` (`{"key": "color-bg", "default": "#fff", "dark": "#12151c"}`).
A theme with at least one has dark mode; one without (like Midnight, which is dark already) shows no switch. A child theme that
changes a colour's default loses the parent's dark value for it, so say its dark value again. Layouts add
`{{template "color-mode-head" .}}` in `<head>`, `{{template "color-toggle" .}}` in the header and
`data-color-mode="{{.Site.ColorMode}}"` on `<html>` (see the default layout).

**Search in themes:** a layout places `{{template "site-search" .}}` where the header box should go (it prints nothing when the owner
has turned search off). The results page is `search/results.html`, which a theme can override like any other template.

**SEO in themes:** a layout puts `{{template "seo-head" .}}` inside `<head>` (from `partials/seo.html`) instead of
writing its own `<title>` and meta tags. Themes that don't will still work, but lose the social and canonical tags.

**Adding a theme:** create `themes/<name>/theme.json` with `"extends": "default"` and the tokens you want to
change, plus a `theme.css`. Anything you don't provide (layout, block and blog templates) is inherited.

## Testing

```bash
go test ./...
```

runs the unit tests. The **integration tests** drive the whole application over HTTP against a real
PostgreSQL database: logins, CSRF, roles, uploads, the blog, themes, domains, security headers. They are
skipped unless you point them at a database whose name ends in `_test` — **each test wipes that database**,
and the tests refuse to run against any other name:

```bash
docker compose up -d db
TEST_DATABASE_URL="postgres://webbuilder:webbuilder@127.0.0.1:5433/webbuilder_test?sslmode=disable" go test ./...
```

(On PowerShell: `$env:TEST_DATABASE_URL = "..."` first.) The database is created if it doesn't exist.

Before a release, also run the race detector and the vulnerability scanner. Both are easiest inside the same
Go image the app is built with:

```bash
docker run --rm --network webbuilder_default -v "$PWD":/src -w /src -e CGO_ENABLED=1 \
  -e TEST_DATABASE_URL="postgres://webbuilder:webbuilder@db:5432/webbuilder_test?sslmode=disable" \
  golang:1.27.2 go test -race ./...
docker run --rm -v "$PWD":/src -w /src golang:1.27.2 go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

If `govulncheck` reports fixes in a newer Go release, bump the version in `Dockerfile` (it is pinned on purpose)
and rebuild with `docker compose build --pull`.

## Security

What protects the site, and where to look:

- **Passwords** are hashed with argon2id; at most 4 hashes run at once so a flood of logins can't exhaust memory.
  Failed logins are limited per address and per account; unknown users cost the same time as wrong passwords.
- **Sessions** live in the database (only a hash of the token is stored), are replaced on login, and end on
  logout, on password change (all other sessions) and when a user is removed.
- **CSRF**: every state-changing request needs a per-session token, and cross-origin browser requests are
  rejected on top of that.
- **Roles**: editors manage content; only admins can change themes, the domain, or users.
- **Content**: rich text is sanitised on save and again on display; link, image and colour values are validated;
  theme tokens are checked against strict patterns before they reach any CSS.
- **Uploads**: type is decided from the bytes (raster images only — never SVG or HTML), images are fully decoded,
  size and pixel counts are capped, stored names are random, files are confined to the media folder and served
  with `nosniff` and a sandboxing CSP.
- **Admin pages** run under a strict Content-Security-Policy (no inline scripts or styles, no framing) with
  `no-referrer`; HSTS is sent over HTTPS. An integration test fails if any admin page gains inline code.
- **Certificates** are only requested for the domain saved in the admin; the check Caddy makes is on a private port.
- **The domain connection check** refuses to connect to non-public addresses and never follows redirects.
- **The container** runs as a non-root user with a read-only filesystem and no capabilities.

Known limitations: login rate limiting is in memory (per process, reset on restart), and an attacker who
knows an admin's email can lock that account out for 15 minutes by failing logins (the cost of limiting by
account). Passwords are checked for length only, not against lists of common passwords. There is no
two-factor authentication and no way for an admin to reset another user's password (remove and re-invite
instead).

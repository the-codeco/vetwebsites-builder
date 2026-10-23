# Existing client sites: what the builder needs to rebuild them

A survey (2026-10-23) of seven live sites made and hosted by vetwebsites.co.za, looked at in a browser
(Playwright) at desktop and phone widths, to find what the builder can already do and what it is missing to
rebuild each one as closely as possible.

All seven are Joomla sites (2.5/3.x era), most on vetwebsites' own Artisteer-style templates. Each has a
"Proudly created by vetwebsites.co.za … [Login]" footer and Google Analytics 4. They are small: 4–25 pages.
Some also show vetwebsites' pet-info articles, which come from a separate API, not from the site (see gap 7).

| Site | Pages | Responsive | Layout |
|---|---|---|---|
| animalhandling.co.za | ~25 | no | boxed on grey; logo + cross-fading photo strip; menu bar; left section sidebar |
| sterkspruitvet.co.za | 7 + pet-info feed | yes | boxed on grey; title on brown panel beside a clinic photo; menu bar; left sidebar (search, contact) |
| joburgpainter.co.za (not a vet) | 4 | yes | boxed on near-black; large banner with logo, menu overlaid on its bottom edge |
| dbayvets.co.za | ~12 | yes | boxed white cards on a photo background; title + tagline over a photo; menu bar; slider |
| northcliffvets.co.za | 7 | yes | full width; menu on top, centred title + "Estd. 1964"; textured blue background image |
| 9thavevet.co.za | ~10 + pet-info feed | no | boxed on green gradient; logo + tagline; menu bar; left sidebar; side art |
| vanriebeeckparkvet.co.za | 6 + pet-info feed | yes | full-width bands; sticky header, logo left, menu right; sliders; textured sections |

## Progress (branch `site-gaps`)

Done:
- **Gap 1, redirects:** query-string redirects and `/*` groups, including in the static export's `.htaccess`.
  Alias domains are not done yet.
- **Gap 2, contact details:** labelled extra numbers (phone, cell, emergency, fax) and email addresses.
- **Gap 3, header layouts:** a logo-and-banner style with cross-fading pictures beside or behind the logo, a centred
  style with the menu bar on top, a tagline, and menu bar colours.
- **Gap 4, fonts:** Arial/Helvetica, Verdana, Tahoma, Trebuchet MS, Times New Roman and Impact.
- **Gap 5, page layout:** a boxed page on a background colour, and a background picture that covers, runs along the
  top or repeats.

Not yet: alias domains, the sidebar (6), slideshow block (8), contact form options (9), tables (10), smaller items (11),
the credit default (12) and the importer (13).

## Gaps, ranked by how many sites need them

1. **Redirects from Joomla addresses (all 7).** Two kinds:
   - Path-style `/index.php/about-us` (and `/index.php/about-us.html`, `/index.php/pet-articles/14-dental-care`):
     the current path-only redirects work, but every page needs its own entry. A prefix rule
     (`/index.php/*` → `/*`, dropping `.html` and `14-` id prefixes) would do most sites in one go.
   - Query-string `/index.php?option=com_content&view=article&id=5&Itemid=105` (animalhandling, 9thavevet,
     vanriebeeckpark, and links inside dbayvets content): redirects refuse `?`. They need to match on the
     query (at least `option` + `id`). The pet-info feed's `?option=com_vetwebsites&view=showArticle&id=N`
     addresses depend on where that separate build puts the articles.
   - Also: alias domains (northcliffvet.co.za without the "s", www variants) should redirect to the main domain.
2. **More contact numbers (6 of 7).** Fax (5 sites), a separate after-hours/emergency number (sterkspruit,
   northcliff, vanriebeeck, dbay), a cellphone, two landlines, and a second email (sterkspruit). Site details has
   one phone + WhatsApp + one email. Needs a list of labelled numbers (Tel / Fax / Cell / Emergency) and labelled
   emails; contact blocks and footer widgets show them with labels or icons.
3. **Header layouts (6 of 7).** The builder only has "logo with the menu beside it". Needed:
   - logo / title + tagline on the left, a banner picture (or cross-fading pictures) filling the rest, the menu as a
     full-width bar underneath (animalhandling, sterkspruit, 9thavevet, dbayvets);
   - a full-width banner image with the logo on it and the menu over its bottom edge (joburgpainter);
   - menu bar on top, a large centred title + tagline below (northcliff);
   - a tagline as text, a search box and social icons inside the menu bar, and an active item highlight colour.
4. **Fonts (6 of 7).** Arial/Helvetica on almost every site; also Tahoma, Verdana, Arial Black, Times. Add these
   web-safe stacks to the font choices (one-line changes); Source Sans Pro (vanriebeeck) can be uploaded as WOFF2.
5. **Boxed layout and page background (6 of 7).** A centred page of ~980px on a background colour (grey, near-black,
   green gradient) or a photo/texture (dbayvets, northcliff, vanriebeeck), with header, menu and footer inside the
   box. A theme setting: "boxed / full width", page background colour, background image (cover, or only the top).
6. **Sidebar shown on every page (4 of 7, + 1 on one page).** Left column with search, contact details, consulting
   hours, an "important notice", photos and partner logos (sterkspruit, 9thavevet, dbayvets' repeated cards); a
   section menu listing the section's pages with the current one marked (animalhandling); a right sidebar on the
   contact page only (vanriebeeck). Best built like the footer builder: a sidebar of widgets (the same widget types,
   plus "section menu" and "search"), on or off per page, with pages belonging to a section (parent page).
7. **Pet-info articles: a separate build, out of scope here.** 9thavevet, sterkspruit and vanriebeeckpark show
   vetwebsites' pet-info articles (`com_vetwebsites`, ~200 articles, categories Breeding, Dental, Diet, Disease,
   Emergency, Eye, General, Heart, Illness, Joints, Lifestyle, Skin, Symptoms, Worms; a paginated card list with a
   category filter bar). These are not the sites' own content: they are fetched from a vetwebsites API, so they are
   not imported or rebuilt as posts. That feed is its own project; the builder will later only need a way to place
   it on a page (and redirects from the old `?option=com_vetwebsites&view=showArticle&id=N` addresses to wherever
   the articles end up). dbayvets' 6 pet articles are its own and become blog posts.
8. **Slideshow / carousel (3 of 7).** A fading banner (animalhandling header, dbayvets home, 5 slides),
   a home slider with arrows and dots (vanriebeeck, 3 slides) and a rotating "services" carousel (vanriebeeck).
   One slideshow block (images + optional text/button per slide, fade or slide, arrows/dots, pauses, respects
   reduced motion) covers all; it can also feed the header banner.
9. **Contact form options (2 of 7).** A required subject and a "send me a copy" checkbox (joburgpainter, 9thavevet).
10. **Tables in the editor (1–2 of 7).** Real product spec tables on animalhandling (TeleDart); elsewhere tables are
    only used for layout and are replaced by blocks. Tiptap needs its table extension, plus table styles.
11. **Smaller items.**
    - Section backgrounds with an image or pattern (vanriebeeck's fur and polygon textures, northcliff): block
      background images.
    - A page title band with a subtitle (vanriebeeck).
    - A site-wide notice or announcement bar (9thavevet's "Important notice").
    - A footer logo/image widget that links to another site (northcliff ePetstore, 9thavevet vetdirectory).
    - A "You are here" breadcrumb (sterkspruit).
    - A visible HTML sitemap page (vanriebeeck).
    - Custom list bullets (joburgpainter, northcliff): custom CSS.
    - A sticky header (vanriebeeck): frozen menus already exist.
    - Round or framed team photos: check the team block's options.
    - A gallery of ~40 photos, possibly with click-to-enlarge (sterkspruit): check the gallery block.
12. **The vetwebsites credit.** Every site has "Proudly created by vetwebsites.co.za" with a Login link. This could be
    a platform default for the footer's bottom line.
13. **Moving content over (all 7).** Copying is manual today. All sites are Joomla on vetwebsites' own hosting and
    templates, so a Joomla importer (the site's own articles → pages and posts, menus, images → media library, and
    the old URL → new URL redirects) would pay off across the client base. It must skip the pet-info feed, which is
    not the site's content.

## Already possible on every site

Text and image pages, Layout columns, team pages (grouped with several team blocks), testimonials, FAQ accordions
(northcliff's broken shortcode FAQ rebuilds as a FAQ block), galleries, video, Google/OpenStreetMap maps, contact
details and form, latest posts, buttons linking to external shops (ePetstore), search, GA4, social links, frozen
menus, phone-friendly menus, and the footer builder (columns, links, hours, logo, coloured background, "Top" link).

## Per-site notes

**animalhandling.co.za.** Boxed on grey; logo beside a cross-fading animal photo strip (an iframe slideshow); a teal
menu bar; a left sidebar that changes by section (11 safety-equipment products, 7 TeleDart products, office hours on
Contact, "Send me a quote" mailto on Ordering), plus search and two photos on every page. Georgia titles, Arial text.
Real spec tables on TeleDart pages; a video. Footer: a teal bar with "TOP", then the hosted-by credit. Old URLs are
query-string only (`index.php?option=com_content&view=article&id=N&Itemid=M`).

**sterkspruitvet.co.za.** Boxed on grey; a brown title panel ("Science in action") beside a clinic photo; a maroon
menu bar; a left sidebar with search and contact (address, email, 2 landlines, after-hours mobile, directions,
Facebook). Home has a ~40-photo gallery. Pet Tips shows the pet-info feed (separate API). About 12 testimonials as
plain text; mixed English/Afrikaans. A breadcrumb bar above the footer. URLs: `/index.php/<alias>.html`.

**joburgpainter.co.za.** Gaobuse Painters, 4 pages. Boxed ~1000px on near-black; a 250px banner image with the logo,
and the orange menu bar plus search over its bottom edge. Steel-blue headings, orange square bullet images. Contact
form with a required subject and "send a copy to yourself". The "request a quote" page is text only. URLs:
`/index.php/<alias>`.

**dbayvets.co.za.** Dana Bay. Boxed white rounded cards with shadows on a fixed autumn photo; the title and tagline as
text over a photo; a dark menu bar with search and Facebook/Google+ badges. Home: a 5-slide fading slider beside
"Consulting hours" and "Contact us" cards that repeat on every page, then three link cards with nested lists.
6 own pet articles, a photo wall, a team of 3 (including the practice cat). Contact: phone, fax, emergency cell.
Fonts: Arial, Tahoma, Verdana, Impact. URLs: `/index.php/<path>`, articles `/index.php/…/pet-articles/14-dental-care`,
plus query-string links in content.

**northcliffvets.co.za.** Full width; the menu at the top right; a big centred title "Northcliff Veterinary Hospital /
Estd. 1964", no logo image; a textured blue background image over the top of the page with white text and white
cards. Home: 3 photo cards, then contact (address, phone, emergency numbers), split consulting hours, and an embedded
Google map. FAQ page of ~20 questions (currently broken shortcodes). ePetstore page with a store code and an external
link; the ePetstore logo as a link in the footer. URLs: `/index.php/<alias>` (`/index.php/pet-info` is the FAQs);
also reached at northcliffvet.co.za.

**9thavevet.co.za.** Not responsive. Boxed ~900px on a green gradient; a tiled green header with the logo and the
tagline "Your pets are ours"; a menu bar. A left sidebar on every page (photo, contact with address/tel/fax/email,
search, consulting hours with an "Important notice", vetdirectory and Facebook links) and decorative cat/dog art in the
right margin. Home: a static banner. Staff in groups (doctors, front, kennel, practice animals). Contact form with a required subject and "send copy to yourself". Shop page is a grid of brand
images. Pet Info shows the pet-info feed (separate API). URLs: query-string only.

**vanriebeeckparkvet.co.za.** RocketTheme (Gantry) template, responsive, full-width bands in red/orange/charcoal with fur and
polygon textures. A sticky header with the logo on the left and the menu on the right; a home slider (3 slides, hexagon dots)
and a services carousel; hexagon icon cards (contact, hours, emergency, location). Inner pages open with a grey title
band. Staff in 3 groups with framed photos. Pet Tips: the pet-info feed (separate API), shown as 20 pages of
2-column dark cards with a category filter. Contact: a right sidebar box (tel, fax, email), a Google map and GPS coordinates.
Footer: business hours, contact, sitemap link, search. URLs: `/index.php/<path>`, articles and category filters by
query string.

package platform

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"webbuilder/internal/admin"
	"webbuilder/internal/auth"
)

// What a website may hold: the server's defaults (QUOTA_STORAGE_MB, QUOTA_PAGES, QUOTA_POSTS), unless the platform
// admin gave that website limits of its own. Zero means no limit. Storage is the pictures and fonts it uploaded.

func (p *Platform) limitsOf(s Site) admin.Limits {
	pick := func(own *int, server int) int {
		if own != nil {
			return *own
		}
		return server
	}
	return admin.Limits{
		StorageBytes: int64(pick(s.QuotaStorageMB, p.cfg.QuotaStorageMB)) << 20,
		Pages:        pick(s.QuotaPages, p.cfg.QuotaPages),
		Posts:        pick(s.QuotaPosts, p.cfg.QuotaPosts),
	}
}

// limitsFunc is what a website's own program asks, each time it is about to add something: the limits as they are
// now, so a change by the admin applies at once without reloading anything.
func (p *Platform) limitsFunc(siteID int64) admin.LimitsFunc {
	return func(ctx context.Context) admin.Limits {
		s, err := p.siteByID(ctx, siteID)
		if err != nil {
			return admin.Limits{} // a limit that cannot be looked up stops nobody
		}
		return p.limitsOf(s)
	}
}

func typed(own *int) string {
	if own == nil {
		return ""
	}
	return strconv.Itoa(*own)
}

func (p *Platform) fillQuota(row *admin.SiteRow, s Site) {
	l := p.limitsOf(s)
	row.LimitPages, row.LimitPosts, row.LimitStorageMB = l.Pages, l.Posts, int(l.StorageBytes>>20)
	row.TypedPages, row.TypedPosts, row.TypedStorageMB = typed(s.QuotaPages), typed(s.QuotaPosts), typed(s.QuotaStorageMB)
}

// usageQuery counts what one website holds. The schema is the platform's own (site_N), and checked anyway.
func usageQuery(id int64, schema string) string {
	return fmt.Sprintf(`SELECT %d AS id,
		(SELECT count(*) FROM "%[2]s".pages) AS pages, (SELECT count(*) FROM "%[2]s".posts) AS posts,
		(SELECT COALESCE(sum(size_bytes), 0) FROM "%[2]s".media) + (SELECT COALESCE(sum(length(data)), 0) FROM "%[2]s".fonts) AS bytes`, id, schema)
}

// fillUsage adds what each website holds to its row. Websites are asked about in groups, so a list of hundreds is a
// handful of queries; one in a bad state is skipped, never fatal.
func (p *Platform) fillUsage(ctx context.Context, sites []Site, rows []admin.SiteRow) {
	at := map[int64]int{}
	for i, s := range sites {
		at[s.ID] = i
	}
	var group []Site
	flush := func() {
		if len(group) == 0 {
			return
		}
		parts := make([]string, len(group))
		for i, s := range group {
			parts[i] = usageQuery(s.ID, s.Schema)
		}
		res, err := p.pool.Query(ctx, strings.Join(parts, " UNION ALL "))
		if err != nil {
			log.Printf("usage: %v", err)
			group = group[:0]
			return
		}
		for res.Next() {
			var id, bytes int64
			var pages, posts int
			if res.Scan(&id, &pages, &posts, &bytes) == nil {
				r := &rows[at[id]]
				r.Pages, r.Posts, r.StorageMB = pages, posts, int(bytes>>20)
			}
		}
		res.Close()
		group = group[:0]
	}
	for _, s := range sites {
		if !schemaRe.MatchString(s.Schema) {
			continue
		}
		group = append(group, s)
		if len(group) == sweepGroup {
			flush()
		}
	}
	flush()
}

// SetLimits sets what a website may hold. A blank value means the server's default; 0 means no limit.
func (p *Platform) SetLimits(ctx context.Context, actor *auth.Identity, siteID int64, storageMB, pages, posts string) error {
	s, err := p.siteByID(ctx, siteID)
	if err != nil {
		return admin.UserError("No such website.")
	}
	parse := func(v string) (*int, error) {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 100_000_000 {
			return nil, admin.UserError("Limits are whole numbers. Leave one blank for the server's default; 0 means no limit.")
		}
		return &n, nil
	}
	st, err := parse(storageMB)
	if err != nil {
		return err
	}
	pg, err := parse(pages)
	if err != nil {
		return err
	}
	po, err := parse(posts)
	if err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `UPDATE sites SET quota_storage_mb = $2, quota_pages = $3, quota_posts = $4 WHERE id = $1`, siteID, st, pg, po); err != nil {
		return err
	}
	p.audit(ctx, actorOf(actor), &s, "changed the limits", fmt.Sprintf("storage %s MB, pages %s, posts %s", orDefault(storageMB), orDefault(pages), orDefault(posts)))
	return nil
}

func orDefault(v string) string {
	if strings.TrimSpace(v) == "" {
		return "default"
	}
	return strings.TrimSpace(v)
}

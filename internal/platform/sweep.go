package platform

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// A lone website runs a few background jobs of its own: publishing scheduled pages and posts, sending queued
// newsletters, and the hourly chores (old sessions and page views, scheduled backups). On a platform there are far
// too many websites to keep every one loaded all the time, so one sweep does the same work for all of them: a cheap
// look at every website's database to see which have something due, and then only those are loaded and served.
const (
	sweepEvery     = 20 * time.Second
	sendBudget     = 10 * time.Second // the most time one website's newsletters get in one sweep, so a big list can't starve the rest
	housekeepEvery = time.Hour
	sweepGroup     = 100 // websites looked at in one query
)

var schemaRe = regexp.MustCompile(`^site_[0-9]+$`)

// dueCheck is what makes a website worth loading right now: scheduled content whose time has come, or newsletter
// messages waiting to be sent. The schema name is the platform's own (site_N), and checked anyway.
func dueCheck(id int64, schema string) string {
	return fmt.Sprintf(`SELECT %d AS id WHERE
		EXISTS (SELECT 1 FROM "%[2]s".pages WHERE status = 'draft' AND publish_at IS NOT NULL AND publish_at <= now())
		OR EXISTS (SELECT 1 FROM "%[2]s".posts WHERE status = 'draft' AND publish_at IS NOT NULL AND publish_at <= now())
		OR EXISTS (SELECT 1 FROM "%[2]s".deliveries d JOIN "%[2]s".campaigns c ON c.id = d.campaign_id
		           WHERE d.status = 'pending' AND d.next_attempt <= now() AND c.status = 'sending')`, id, schema)
}

// dueSites lists the active websites with something due.
func (p *Platform) dueSites(ctx context.Context) ([]Site, error) {
	all, err := p.listSites(ctx)
	if err != nil {
		return nil, err
	}
	var active []Site
	for _, s := range all {
		if s.Active && schemaRe.MatchString(s.Schema) {
			active = append(active, s)
		}
	}
	var due []Site
	for start := 0; start < len(active); start += sweepGroup {
		group := active[start:min(start+sweepGroup, len(active))]
		parts := make([]string, len(group))
		byID := map[int64]Site{}
		for i, s := range group {
			parts[i] = dueCheck(s.ID, s.Schema)
			byID[s.ID] = s
		}
		rows, err := p.pool.Query(ctx, strings.Join(parts, " UNION ALL "))
		if err != nil { // one website in a bad state must not stop the others: ask about them one by one
			for _, s := range group {
				var id int64
				if p.pool.QueryRow(ctx, dueCheck(s.ID, s.Schema)).Scan(&id) == nil {
					due = append(due, s)
				}
			}
			continue
		}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				due = append(due, byID[id])
			}
		}
		rows.Close()
	}
	return due, nil
}

// SweepOnce does the work that is due for every active website: scheduled pages and posts go live, and queued
// newsletters are sent. It returns how many websites it served. Run calls it every few seconds; tests call it directly.
func (p *Platform) SweepOnce(ctx context.Context) int {
	due, err := p.dueSites(ctx)
	if err != nil {
		log.Printf("sweep: %v", err)
		return 0
	}
	for _, s := range due {
		if ctx.Err() != nil {
			break
		}
		p.serve(ctx, s)
	}
	return len(due)
}

func (p *Platform) serve(ctx context.Context, s Site) {
	rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
	if err != nil {
		log.Printf("sweep: site %d: %v", s.ID, err)
		return
	}
	defer release()

	pages, posts, err := rt.App.PublishDue(ctx)
	if err != nil {
		log.Printf("sweep: site %d: scheduled publishing: %v", s.ID, err)
	} else if pages+posts > 0 {
		p.audit(ctx, "system", &s, "scheduled content went live", fmt.Sprintf("%d page(s), %d post(s)", pages, posts))
		p.autoPublish(ctx, s)
	}
	if _, err := rt.App.SendQueuedMail(ctx, sendBudget); err != nil && ctx.Err() == nil {
		log.Printf("sweep: site %d: newsletters: %v", s.ID, err)
	}
}

// autoPublish puts a website's changes on its hosting when scheduled content has just gone live, if its owner asked
// for that. Everything unpublished goes with it: that is what "publish" means.
func (p *Platform) autoPublish(ctx context.Context, s Site) {
	t, ok, err := p.loadTarget(ctx, s.ID)
	if err != nil || !ok || !t.AutoPublish || s.PublicURL == "" {
		return
	}
	// after a rollback the owner chose the version that is up: scheduled content must not quietly replace it
	var back bool
	p.pool.QueryRow(ctx, `SELECT restore_version <> '' FROM publish_jobs WHERE site_id = $1 AND status = 'done' ORDER BY id DESC LIMIT 1`, s.ID).Scan(&back)
	if back {
		return
	}
	if _, err := (&sitePublisher{p, s.ID}).Start(ctx, "system (scheduled content)", false); err != nil {
		log.Printf("sweep: site %d: automatic publish: %v", s.ID, err)
	}
}

// HousekeepOnce does the hourly chores for every active website: old sessions and page views, unconfirmed
// signups, and the scheduled backup when one is due.
func (p *Platform) HousekeepOnce(ctx context.Context) {
	all, err := p.listSites(ctx)
	if err != nil {
		log.Printf("housekeeping: %v", err)
		return
	}
	for _, s := range all {
		if ctx.Err() != nil {
			return
		}
		if !s.Active {
			continue
		}
		rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
		if err != nil {
			log.Printf("housekeeping: site %d: %v", s.ID, err)
			continue
		}
		if err := rt.App.Housekeep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("housekeeping: site %d: %v", s.ID, err)
		}
		release()
		select { // spread the load: a few hundred websites are not all loaded at the same moment
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// sweepLoop and housekeepLoop run until ctx ends.
func (p *Platform) sweepLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(sweepEvery):
		}
		p.SweepOnce(ctx)
	}
}

func (p *Platform) housekeepLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute): // let the server settle after a start
	}
	for {
		p.HousekeepOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(housekeepEvery):
		}
	}
}

// FlushSite writes the page views a website is still holding in memory. They are normally written every few seconds;
// this is for tests and for anything that needs the numbers right now.
func (p *Platform) FlushSite(ctx context.Context, siteID int64) error {
	s, err := p.siteByID(ctx, siteID)
	if err != nil {
		return err
	}
	rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
	if err != nil {
		return err
	}
	defer release()
	return rt.App.FlushAnalytics(ctx)
}

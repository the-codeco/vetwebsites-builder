package content

import (
	"context"
	"errors"
	"time"
)

// MaxScheduleAhead is how far in the future a page or post can be scheduled.
const MaxScheduleAhead = 2 * 366 * 24 * time.Hour

// ErrBadSchedule is returned for a publish time that is not in the future (or is absurdly far off).
var ErrBadSchedule = errors.New("choose a time in the future, within two years")

// CheckSchedule validates the time a draft is to be published at. nil means no schedule.
func CheckSchedule(at *time.Time, now time.Time) error {
	if at == nil {
		return nil
	}
	if !at.After(now) || at.After(now.Add(MaxScheduleAhead)) {
		return ErrBadSchedule
	}
	return nil
}

// PublishDue publishes every draft whose time has come and returns how many pages and posts it published. A post
// takes its scheduled time as its publication date, so it sorts where its author meant it to.
func (s *Store) PublishDue(ctx context.Context) (pages, posts int, err error) {
	if err = s.pool.QueryRow(ctx, `
		WITH d AS (UPDATE pages SET status = 'published', publish_at = NULL, updated_at = now()
		           WHERE status = 'draft' AND publish_at IS NOT NULL AND publish_at <= now() RETURNING 1)
		SELECT count(*) FROM d`).Scan(&pages); err != nil {
		return
	}
	err = s.pool.QueryRow(ctx, `
		WITH d AS (UPDATE posts SET status = 'published', published_at = publish_at, publish_at = NULL, updated_at = now()
		           WHERE status = 'draft' AND publish_at IS NOT NULL AND publish_at <= now() RETURNING 1)
		SELECT count(*) FROM d`).Scan(&posts)
	return
}

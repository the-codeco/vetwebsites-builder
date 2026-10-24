package admin

import (
	"context"
	"fmt"
)

// Limits are what a website may hold. Zero means no limit. A lone website has none; a platform sets them for each
// website (see Deps.Limits).
type Limits struct {
	StorageBytes int64 // the pictures and fonts it has uploaded
	Pages, Posts int
}

// LimitsFunc says what a website may hold right now.
type LimitsFunc func(ctx context.Context) Limits

func megabytes(b int64) string {
	switch {
	case b >= 10<<20:
		return fmt.Sprintf("%d MB", b>>20)
	case b > 0:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	}
	return "0 MB"
}

// limitProblem says why one more of something cannot be added, or "" when it can. what is "pages", "posts" or
// "storage"; addBytes is the size of the file about to be stored. A website with no limits, and a limit that cannot be
// looked up, never stops anyone: running out of room is announced, not guessed at.
func (h *Handler) limitProblem(ctx context.Context, what string, addBytes int64) string {
	if h.Limits == nil {
		return ""
	}
	lim := h.Limits(ctx)
	u, err := h.Content.Usage(ctx)
	if err != nil {
		return ""
	}
	switch what {
	case "pages":
		if lim.Pages > 0 && u.Pages >= lim.Pages {
			return fmt.Sprintf("Your website has reached its limit of %d pages. Delete a page you no longer need, or ask us to raise the limit.", lim.Pages)
		}
	case "posts":
		if lim.Posts > 0 && u.Posts >= lim.Posts {
			return fmt.Sprintf("Your website has reached its limit of %d blog posts. Delete a post you no longer need, or ask us to raise the limit.", lim.Posts)
		}
	case "storage":
		if lim.StorageBytes > 0 && u.StorageBytes+addBytes > lim.StorageBytes {
			return fmt.Sprintf("Your picture storage is full (%s of %s used). Delete pictures you no longer use, or ask us to raise the limit.", megabytes(u.StorageBytes), megabytes(lim.StorageBytes))
		}
	}
	return ""
}

// QuotaLine is one limit as the dashboard shows it.
type QuotaLine struct {
	Label      string
	Used, Of   string
	Value, Max int64 // for the bar
	NearlyFull bool  // at least 90% used
	Full       bool
}

// quotaLines are the website's limits and how much of each it has used; none when it has no limits.
func (h *Handler) quotaLines(ctx context.Context) []QuotaLine {
	if h.Limits == nil {
		return nil
	}
	lim := h.Limits(ctx)
	u, err := h.Content.Usage(ctx)
	if err != nil {
		return nil
	}
	var out []QuotaLine
	add := func(label string, used, limit int64, show func(int64) string) {
		if limit <= 0 {
			return
		}
		out = append(out, QuotaLine{Label: label, Used: show(used), Of: show(limit), Value: min(used, limit), Max: limit,
			NearlyFull: used*10 >= limit*9, Full: used >= limit})
	}
	count := func(n int64) string { return fmt.Sprint(n) }
	add("Pages", int64(u.Pages), int64(lim.Pages), count)
	add("Blog posts", int64(u.Posts), int64(lim.Posts), count)
	add("Picture storage", u.StorageBytes, lim.StorageBytes, megabytes)
	return out
}

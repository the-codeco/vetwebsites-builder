package admin

import (
	"net/http"
	"strings"
	"time"

	"webbuilder/internal/content"
)

// publishAtFrom reads the time a draft is to be published at from a form. The browser sends it as a UTC time in
// "publish_at" (a small script converts what the person typed in their own time zone). Without the script, the
// typed value in "publish_local" is read as UTC. Nothing is scheduled unless the status is draft. The returned
// message is for the person when the time is not usable.
func publishAtFrom(r *http.Request, status string, now time.Time) (*time.Time, string) {
	if status != "draft" {
		return nil, ""
	}
	raw := strings.TrimSpace(r.PostFormValue("publish_at"))
	layout := time.RFC3339
	if raw == "" {
		raw, layout = strings.TrimSpace(r.PostFormValue("publish_local")), "2006-01-02T15:04"
	}
	if raw == "" {
		return nil, ""
	}
	t, err := time.Parse(layout, raw)
	if err != nil {
		return nil, "Choose a valid date and time to publish at."
	}
	t = t.UTC().Truncate(time.Minute)
	if content.CheckSchedule(&t, now) != nil {
		return nil, "The time to publish at must be in the future, within two years."
	}
	return &t, ""
}

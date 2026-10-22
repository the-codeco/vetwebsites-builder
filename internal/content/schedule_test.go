package content

import (
	"testing"
	"time"
)

func TestCheckSchedule(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(d); return &x }
	if CheckSchedule(nil, now) != nil {
		t.Error("no schedule is fine")
	}
	for name, c := range map[string]struct {
		at *time.Time
		ok bool
	}{
		"a minute away": {at(time.Minute), true}, "tomorrow": {at(24 * time.Hour), true}, "just under two years": {at(MaxScheduleAhead - time.Hour), true},
		"now": {at(0), false}, "a second ago": {at(-time.Second), false}, "last year": {at(-365 * 24 * time.Hour), false}, "three years": {at(3 * 366 * 24 * time.Hour), false},
		"just over two years": {at(MaxScheduleAhead + time.Hour), false},
	} {
		if got := CheckSchedule(c.at, now) == nil; got != c.ok {
			t.Errorf("%s: accepted=%v, want %v", name, got, c.ok)
		}
	}
}

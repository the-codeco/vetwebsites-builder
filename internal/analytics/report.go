package analytics

import (
	"context"
	"time"
)

// Day is one bar of the chart.
type Day struct {
	Date            time.Time
	Views, Visitors int
}

// Count is one row of a "top …" table.
type Count struct {
	Name            string
	Views, Visitors int
}

// Report is everything the analytics screen shows for a period.
type Report struct {
	Days            int
	From, To        time.Time // first and last day, UTC
	Views, Visitors int       // Visitors adds up each day's unique visitors
	Direct          int       // views with no referring site
	Series          []Day     // one per day, zeros included
	Pages           []Count
	Referrers       []Count
	Devices         []Count
	Browsers        []Count
}

// Report summarises the last `days` days (today included).
func (s *Service) Report(ctx context.Context, days int) (Report, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	r := Report{Days: days, To: today, From: today.AddDate(0, 0, -(days - 1))}
	from := r.From.Format("2006-01-02")

	rows, err := s.pool.Query(ctx, `
		SELECT day, count(*), count(DISTINCT visitor) FROM hits WHERE day >= $1 GROUP BY day`, from)
	if err != nil {
		return r, err
	}
	byDay := map[string]Day{}
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.Date, &d.Views, &d.Visitors); err != nil {
			rows.Close()
			return r, err
		}
		byDay[d.Date.Format("2006-01-02")] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, err
	}
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		day := byDay[d.Format("2006-01-02")]
		day.Date = d
		r.Series = append(r.Series, day)
		r.Views += day.Views
		r.Visitors += day.Visitors
	}

	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM hits WHERE day >= $1 AND referrer = ''`, from).Scan(&r.Direct); err != nil {
		return r, err
	}
	for _, q := range []struct {
		col   string
		where string
		into  *[]Count
	}{
		{"path", "", &r.Pages},
		{"referrer", "AND referrer <> ''", &r.Referrers},
		{"device", "", &r.Devices},
		{"browser", "", &r.Browsers},
	} {
		// col and where come from the literals above, never from a request
		list, err := s.top(ctx, q.col, q.where, from)
		if err != nil {
			return r, err
		}
		*q.into = list
	}
	return r, nil
}

func (s *Service) top(ctx context.Context, col, where, from string) ([]Count, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+col+`, count(*), count(DISTINCT visitor) FROM hits
		WHERE day >= $1 `+where+` GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT `+"10", from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Views, &c.Visitors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

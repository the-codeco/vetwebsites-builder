package app

import (
	"context"
	"net/http"

	"webbuilder/internal/content"
	"webbuilder/internal/export"
)

// ExportSource is this website as the exporter sees it.
func (a *App) ExportSource() export.Source { return exportSource{a} }

type exportSource struct{ a *App }

func (s exportSource) Handler() http.Handler { return s.a.Handler }

func (s exportSource) Paths(ctx context.Context, perPage int) ([]string, error) {
	return s.a.content.ExportPaths(ctx, perPage)
}

func (s exportSource) SearchIndex(ctx context.Context, maxText int) ([]export.Entry, error) {
	rows, err := s.a.content.SearchIndex(ctx, maxText)
	if err != nil {
		return nil, err
	}
	out := make([]export.Entry, len(rows))
	for i, r := range rows {
		out[i] = export.Entry{Path: r.Path, Title: r.Title, Kind: r.Kind, Text: r.Text}
	}
	return out, nil
}

func (s exportSource) Redirects(ctx context.Context) ([]export.Redirect, error) {
	rows, err := s.a.content.RedirectList(ctx)
	if err != nil {
		return nil, err
	}
	return toExportRedirects(rows), nil
}

func toExportRedirects(rows []content.Redirect) []export.Redirect {
	out := make([]export.Redirect, len(rows))
	for i, r := range rows {
		out[i] = export.Redirect{From: r.From, To: r.To, Permanent: r.Permanent}
	}
	return out
}

func (s exportSource) Changes(ctx context.Context) (int64, error) {
	return s.a.content.ChangeCount(ctx)
}

// ChangeCount is the website's change counter (see migration 00024).
func (a *App) ChangeCount(ctx context.Context) (int64, error) { return a.content.ChangeCount(ctx) }

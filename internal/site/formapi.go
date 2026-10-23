package site

import (
	"encoding/json"
	"html"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"webbuilder/internal/search"
)

// FormAPI is what a published static copy of the website talks to for its contact and newsletter forms: the copy
// has no server of its own, so its forms are sent here, to the builder. The paths are relative to the website:
//
//	GET  /token      a fresh token for each form, fetched when the page is opened
//	POST /contact    a contact message (the same fields and checks as the live form)
//	POST /subscribe  a newsletter signup
//	GET  /search     the website's search (q and page), as JSON
//
// A request that asks for JSON gets {"ok":true} or {"ok":false,"error":"…"}; any other (a form sent without
// JavaScript) gets a short page. Who may call it (cross-origin rules) is the caller's business.
func (s *Service) FormAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /token", s.formToken)
	mux.HandleFunc("GET /search", s.formSearch)
	mux.HandleFunc("POST /contact", func(w http.ResponseWriter, r *http.Request) {
		s.formPost(w, r, s.Contact != nil, "Thank you, your message has been sent.", s.acceptContact)
	})
	mux.HandleFunc("POST /subscribe", func(w http.ResponseWriter, r *http.Request) {
		s.formPost(w, r, s.Newsletter != nil, "Thank you for subscribing.", s.acceptSubscribe)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Service) formToken(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	now := time.Now()
	if s.Contact != nil {
		out["contact"] = s.Contact.Token(now)
	}
	if s.Newsletter != nil {
		out["subscribe"] = s.Newsletter.Token(now)
	}
	writeJSON(w, http.StatusOK, out)
}

type snippetPart struct {
	T string `json:"t"`
	M bool   `json:"m,omitempty"` // a matched word
}

type searchHit struct {
	Kind    string        `json:"kind"`
	Title   string        `json:"title"`
	Path    string        `json:"path"`
	Snippet []snippetPart `json:"snippet"`
}

// snippetParts splits a snippet (escaped HTML with <mark> around matched words) into plain text pieces, so the
// published page can show it without ever treating it as markup.
func snippetParts(h template.HTML) []snippetPart {
	var out []snippetPart
	rest := string(h)
	for rest != "" {
		i := strings.Index(rest, "<mark>")
		if i < 0 {
			out = append(out, snippetPart{T: html.UnescapeString(rest)})
			break
		}
		if i > 0 {
			out = append(out, snippetPart{T: html.UnescapeString(rest[:i])})
		}
		rest = rest[i+len("<mark>"):]
		j := strings.Index(rest, "</mark>")
		if j < 0 {
			j = len(rest)
		}
		out = append(out, snippetPart{T: html.UnescapeString(rest[:j]), M: true})
		rest = strings.TrimPrefix(rest[j:], "</mark>")
	}
	return out
}

// formSearch answers the search box of the published copy with the same results as the live site's /search page.
func (s *Service) formSearch(w http.ResponseWriter, r *http.Request) {
	if s.Search == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Search is switched off."})
		return
	}
	q := search.Clean(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	out := map[string]any{"query": q, "total": 0, "page": 1, "pages": 1, "items": []searchHit{}}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if !s.Search.Allow(r) {
		out["limited"] = true
		writeJSON(w, http.StatusTooManyRequests, out)
		return
	}
	res, err := s.Search.Run(r.Context(), q, max(page, 1))
	if err != nil {
		log.Printf("search: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Search is not available right now."})
		return
	}
	hits := []searchHit{}
	for _, it := range res.Items {
		hits = append(hits, searchHit{Kind: it.Kind, Title: it.Title, Path: it.Path, Snippet: snippetParts(it.Snippet)})
	}
	out["total"], out["page"], out["pages"], out["items"] = res.Total, res.Page, res.Pages, hits
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) formPost(w http.ResponseWriter, r *http.Request, enabled bool, thanks string, accept func(*http.Request) submitResult) {
	wantsJSON := strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
	reply := func(status int, msg string, ok bool) {
		if wantsJSON {
			body := map[string]any{"ok": ok}
			if !ok {
				body["error"] = msg
			}
			writeJSON(w, status, body)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		title := "Sent"
		if !ok {
			title = "Not sent"
		}
		w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">` +
			`<title>` + title + `</title><body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><h1>` + title +
			`</h1><p>` + html.EscapeString(msg) + `</p><p>Use your browser's back button to return to the website.</p></body></html>`))
	}
	if !enabled {
		reply(http.StatusNotFound, "This form is switched off.", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		reply(http.StatusBadRequest, "The form could not be read.", false)
		return
	}
	res := accept(r)
	if res.Problem != "" {
		reply(res.Status, res.Problem, false)
		return
	}
	reply(http.StatusOK, thanks, true) // a bot's submission (Silent) is answered the same way
}

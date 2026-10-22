package app_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRepeatingListsEditSaveAndShow(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Lists")
	admin.post("/admin/pages/"+id, url.Values{"title": {"Lists"}, "slug": {"lists"}, "status": {"published"}})
	if r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"faq"}}); r.Status != 200 {
		t.Fatalf("add: %d", r.Status)
	}
	faq := e.blockID(id, "faq")

	// the editor offers an empty list, a blank item to copy, and the add / move / remove buttons
	page := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{`data-repeater data-max="200"`, `<template data-rep-template>`, `name="p.items.__N__.question"`, `data-rep="add">+ Add question`, `data-rep="up"`, `data-rep="down"`, `data-rep="remove"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the editor lacks %s", want)
		}
	}

	// far more than the old ten questions, written in a different order from their numbers
	form := url.Values{"p.heading": {"Many"}}
	for i := 0; i < 40; i++ {
		n := 100 - i*2 // numbers need only be in order, not consecutive
		form.Set(fmt.Sprintf("p.items.%d.question", n), fmt.Sprintf("Question number %02d?", 39-i))
		form.Set(fmt.Sprintf("p.items.%d.answer", n), "Answer <script>alert(1)</script>")
	}
	form.Set("p.items.5.question", "") // blank items are dropped
	form.Set("p.items.5.answer", "")
	if r := admin.post("/admin/blocks/"+faq, form); r.Status != 200 || !strings.Contains(r.Body, "Saved") {
		t.Fatalf("save: %d", r.Status)
	}
	if got := e.sql(`SELECT jsonb_array_length(props->'items') FROM blocks WHERE id = ` + faq); got != "40" {
		t.Errorf("forty items stored, got %s", got)
	}
	if got := e.sql(`SELECT props->'items'->0->>'question' || ' / ' || (props->'items'->39->>'question') FROM blocks WHERE id = ` + faq); got != "Question number 00? / Question number 39?" {
		t.Errorf("order follows the numbers: %q", got)
	}

	// the saved items come back in the editor, numbered from 0 in order
	card := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{`name="p.items.0.question"`, `value="Question number 00?"`, `name="p.items.39.question"`, `>40</span>`} {
		if !strings.Contains(card, want) {
			t.Errorf("editor lacks %s", want)
		}
	}
	if strings.Contains(card, `name="p.items.40.question"`) {
		t.Error("only the forty saved items are listed")
	}

	body := anon.get("/lists").Body
	if strings.Count(body, `class="faq-item"`) != 40 || !strings.Contains(body, "Question number 39?") || strings.Contains(body, "<script>alert") {
		t.Errorf("public page: %d items", strings.Count(body, `class="faq-item"`))
	}
	if r := anon.get("/search?q=%22number+17%22"); !strings.Contains(r.Body, "Lists") {
		t.Error("words in list items are searchable")
	}

	// removing items is saving fewer; saving none empties the list
	admin.post("/admin/blocks/"+faq, url.Values{"p.items.0.question": {"Only one"}, "p.items.0.answer": {"Yes"}})
	if got := e.sql(`SELECT jsonb_array_length(props->'items') FROM blocks WHERE id = ` + faq); got != "1" {
		t.Errorf("one item, got %s", got)
	}
	admin.post("/admin/blocks/"+faq, url.Values{"p.heading": {"None"}})
	if got := e.sql(`SELECT props->>'items' FROM blocks WHERE id = ` + faq); got != "[]" {
		t.Errorf("an empty list is [], got %q", got)
	}

	// a hand-made request cannot make a block enormous, or send junk names
	huge := url.Values{"p.items.abc.question": {"junk"}, "p.items.1.2.question": {"junk"}, "p.items..question": {"junk"}}
	for i := 0; i < 300; i++ {
		huge.Set(fmt.Sprintf("p.items.%d.question", i), "Q")
	}
	admin.post("/admin/blocks/"+faq, huge)
	if got := e.sql(`SELECT jsonb_array_length(props->'items') FROM blocks WHERE id = ` + faq); got != "200" {
		t.Errorf("a list stops at 200 items, got %s", got)
	}
	// a viewer cannot save lists
	if r := e.client().post("/admin/blocks/"+faq, url.Values{"p.items.0.question": {"x"}}); r.Status == 200 {
		t.Error("not logged in")
	}
}

func TestListsInsideLayoutsAndBackups(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	pageID, layout := layoutPage(t, e, admin, "Listed")
	feat := e.addChild(t, admin, layout, 1, "features")
	form := url.Values{"p.columns": {"2"}}
	for i := 0; i < 12; i++ {
		form.Set(fmt.Sprintf("p.cards.%d.title", i), fmt.Sprintf("Card %d", i))
		form.Set(fmt.Sprintf("p.cards.%d.link", i), "/c")
	}
	form.Set("p.cards.3.link", "javascript:alert(1)")
	e.saveChild(t, admin, feat, form)
	admin.post("/admin/pages/"+pageID, url.Values{"title": {"Listed"}, "slug": {"listed"}, "status": {"published"}})

	body := anon.get("/listed").Body
	if strings.Count(body, `<article class="feature">`) != 12 || strings.Contains(body, "javascript:") || strings.Count(body, `class="feature-link"`) != 11 {
		t.Errorf("twelve cards inside a column, one without a safe link")
	}

	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := e.sql(`SELECT string_agg(props::text, E'\n' ORDER BY id) FROM blocks WHERE type = 'features'`)
	raw := []byte(admin.downloadBackup(false).Body)
	if !strings.Contains(string(unzip(t, raw)["site.json"]), `"Card 11"`) {
		t.Error("the backup holds the list")
	}
	e.pool.Exec(t.Context(), `DELETE FROM pages WHERE slug = 'listed'`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := e.sql(`SELECT string_agg(props::text, E'\n' ORDER BY id) FROM blocks WHERE type = 'features'`); after != before {
		t.Errorf("lists changed in the round trip:\n%s\n%s", before, after)
	}
	if got := anon.get("/listed").Body; strings.Count(got, `<article class="feature">`) != 12 {
		t.Error("restored cards are shown")
	}
}

func TestOldNumberedBlocksStillRestoreAndShow(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Old")
	admin.post("/admin/pages/"+id, url.Values{"title": {"Old"}, "slug": {"old"}, "status": {"published"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"faq"}})
	faq := e.blockID(id, "faq")
	// a block saved before lists existed
	e.pool.Exec(t.Context(), `UPDATE blocks SET props = '{"heading":"Old FAQ","q1_question":"Legacy question?","q1_answer":"<p>Legacy answer</p>","q2_question":"Second legacy?"}' WHERE id = `+faq)
	body := anon.get("/old").Body
	if !strings.Contains(body, "Legacy question?") || !strings.Contains(body, "Second legacy?") || strings.Count(body, `class="faq-item"`) != 2 {
		t.Errorf("an old FAQ is still shown:\n%s", body)
	}
	if page := admin.get("/admin/pages/" + id).Body; !strings.Contains(page, `value="Legacy question?"`) || !strings.Contains(page, `name="p.items.1.question"`) {
		t.Error("an old FAQ opens in the editor as a list")
	}
	admin.post("/admin/blocks/"+faq, url.Values{"p.items.0.question": {"Legacy question?"}, "p.items.0.answer": {"x"}})
	if got := e.sql(`SELECT props ? 'q1_question' FROM blocks WHERE id = ` + faq); got != "false" {
		t.Error("saving drops the old numbered fields")
	}
}

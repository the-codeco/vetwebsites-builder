package blocks

import (
	"sync"
	"time"
)

// Drafts holds block edits that have been typed but not saved, so the editor's live preview can show them. A draft
// belongs to one person: only that person's preview applies it, never a visitor's page or an exported copy. It
// disappears when the block is saved, when the person opens the editor again, or after a quarter of an hour.
type Drafts struct {
	mu  sync.Mutex
	m   map[draftKey]draft
	now func() time.Time
}

type draftKey struct{ user, block int64 }

type draft struct {
	props map[string]any
	style Style
	at    time.Time
}

const (
	draftTTL  = 15 * time.Minute
	maxDrafts = 2000 // in all, so a stuck script cannot fill the memory
)

func NewDrafts() *Drafts { return &Drafts{m: map[draftKey]draft{}, now: time.Now} }

func (d *Drafts) prune() {
	cut := d.now().Add(-draftTTL)
	for k, v := range d.m {
		if v.at.Before(cut) {
			delete(d.m, k)
		}
	}
}

// Set remembers one person's unsaved edit of a block. props and style must already be cleaned.
func (d *Drafts) Set(user, block int64, props map[string]any, style Style) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.m) >= maxDrafts {
		d.prune()
	}
	k := draftKey{user, block}
	if _, have := d.m[k]; !have && len(d.m) >= maxDrafts {
		return
	}
	d.m[k] = draft{props: props, style: style, at: d.now()}
}

func (d *Drafts) Clear(user, block int64) {
	d.mu.Lock()
	delete(d.m, draftKey{user, block})
	d.mu.Unlock()
}

// ClearUser forgets everything one person has not saved.
func (d *Drafts) ClearUser(user int64) {
	d.mu.Lock()
	for k := range d.m {
		if k.user == user {
			delete(d.m, k)
		}
	}
	d.mu.Unlock()
}

// Apply returns the blocks as one person is editing them: their unsaved edits replace the saved ones, inside layouts
// too. The blocks passed in are not changed.
func (d *Drafts) Apply(user int64, in []Block) []Block {
	d.mu.Lock()
	d.prune()
	mine := map[int64]draft{}
	for k, v := range d.m {
		if k.user == user {
			mine[k.block] = v
		}
	}
	d.mu.Unlock()
	if len(mine) == 0 {
		return in
	}
	return applyDrafts(mine, in)
}

func applyDrafts(mine map[int64]draft, in []Block) []Block {
	out := make([]Block, len(in))
	for i, b := range in {
		if dr, ok := mine[b.ID]; ok {
			b.Props, b.Style = dr.props, dr.style
		}
		if b.Children != nil {
			cols := make([][]Block, len(b.Children))
			for j, col := range b.Children {
				cols[j] = applyDrafts(mine, col)
			}
			b.Children = cols
		}
		out[i] = b
	}
	return out
}

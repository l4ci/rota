package cli

import (
	"bytes"
	"errors"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tui"
)

// The read-only --ui views (#546) are one screen: a filterable list and a
// reader, built from a verb's Data. A view differs from the next only in its
// rows and in how a row's full text is fetched, so the screen is shared.

// browseRow is one list row. Preview is what the reader shows until the row is
// opened; Key names the row to the loader.
type browseRow struct {
	Label, Preview, Key string
}

// bodyLoader fetches a row's full text. It runs in a Cmd, never in Update.
type bodyLoader func(key string) (string, error)

// bodyCache is shared by every copy of the model: Update stays pure and the
// loader's Cmd writes here, from the driver's goroutine.
type bodyCache struct {
	mu   sync.Mutex
	text map[string]string
}

func (b *bodyCache) get(k string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.text[k]
	return v, ok
}

func (b *bodyCache) put(k, v string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text[k] = v
}

// browser is a list with a reader beside it, or under it when Stacked.
type browser struct {
	Title   string
	Empty   string // shown when there are no rows
	Rows    []browseRow
	Stacked bool
	Load    bodyLoader // nil: the reader only shows Preview

	list    tui.List
	reader  tui.Detail
	opened  bool // the reader holds the loaded text and has the keys
	pending string
	err     string
	cache   *bodyCache
}

func newBrowser(title, empty string, rows []browseRow, stacked bool, load bodyLoader) browser {
	for i := range rows {
		rows[i].Label, rows[i].Preview = tui.Sanitize(strings.ReplaceAll(rows[i].Label, "\n", " "), true), tui.Sanitize(rows[i].Preview, true)
	}
	b := browser{Title: title, Empty: empty, Rows: rows, Stacked: stacked, Load: load, cache: &bodyCache{text: map[string]string{}}}
	for _, r := range rows {
		b.list.Items = append(b.list.Items, r.Label)
	}
	return b.preview()
}

func (b browser) current() (browseRow, bool) {
	i, ok := b.list.Selected()
	if !ok {
		return browseRow{}, false
	}
	return b.Rows[i], true
}

// preview resets the reader to the selected row's preview.
func (b browser) preview() browser {
	b.opened, b.err = false, ""
	b.reader = tui.Detail{}
	if r, ok := b.current(); ok {
		b.reader.Body = r.Preview
		if !b.Stacked {
			b.reader.Title = r.Label
		}
	}
	return b
}

func (b browser) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch m := msg.(type) {
	case tui.Done:
		if m.Err != nil {
			b.err = m.Err.Error()
			return b, tui.Cmd{}
		}
		if r, ok := b.current(); ok && r.Key == b.pending {
			if text, ok := b.cache.get(r.Key); ok {
				b.reader.Body, b.reader.Top, b.opened = text, 0, true
			}
		}
		return b, tui.Cmd{}
	case tui.Key:
		return b.key(m)
	}
	return b, tui.Cmd{}
}

func (b browser) key(k tui.Key) (tui.Model, tui.Cmd) {
	if b.opened {
		switch {
		case k.Kind == tui.KeyEsc || k.Kind == tui.KeyBackspace || k.Kind == tui.KeyLeft || k.Is('q'):
			return b.preview(), tui.Cmd{}
		case k.Kind == tui.KeyCtrlC:
			return b, tui.Cmd{Quit: true}
		}
		b.reader, _ = b.reader.Update(k)
		return b, tui.Cmd{}
	}
	// Esc first clears an applied filter; q and Ctrl-C always go back.
	if !b.list.Filtering && (k.Is('q') || k.Kind == tui.KeyCtrlC || (k.Kind == tui.KeyEsc && b.list.Filter == "")) {
		return b, tui.Cmd{Quit: true}
	}
	if k.Kind == tui.KeyEnter && !b.list.Filtering {
		return b.open()
	}
	before, _ := b.list.Selected()
	l, _ := b.list.Update(k)
	b.list = l
	if after, _ := b.list.Selected(); after != before {
		b = b.preview()
	}
	return b, tui.Cmd{}
}

// open reads the selected row in full: from the cache, else through the loader.
func (b browser) open() (tui.Model, tui.Cmd) {
	r, ok := b.current()
	if !ok || b.Load == nil {
		return b, tui.Cmd{}
	}
	b.pending, b.err = r.Key, ""
	if text, ok := b.cache.get(r.Key); ok {
		b.reader.Body, b.reader.Top, b.opened = text, 0, true
		return b, tui.Cmd{}
	}
	load, cache, key := b.Load, b.cache, r.Key
	return b, tui.Cmd{Exec: func() error {
		text, err := load(key)
		if err == nil {
			cache.put(key, tui.Sanitize(text, true))
		}
		return err
	}}
}

func (b browser) hints() []tui.Hint {
	switch {
	case b.opened:
		return []tui.Hint{{Key: "j/k", Desc: "scroll"}, {Key: "Esc", Desc: "back to list"}}
	case b.Load != nil:
		return []tui.Hint{{Key: "j/k", Desc: "move"}, {Key: "Enter", Desc: "read"}, {Key: "/", Desc: "filter"}, {Key: "q", Desc: "back"}}
	}
	return []tui.Hint{{Key: "j/k", Desc: "move"}, {Key: "/", Desc: "filter"}, {Key: "q", Desc: "back"}}
}

func (b browser) Render(w, h int, st tui.Style) string {
	head := []string{st.Bold(b.Title), st.Dim(b.list.FilterLine())}
	room := 0
	if h > 0 {
		room = max(h-1-len(head), 1)
	}
	var body string
	switch {
	case len(b.Rows) == 0:
		body = st.Dim("  " + b.Empty)
	case b.Stacked:
		const readerRows = 5
		listRows := room
		if room > 0 {
			listRows = max(room-readerRows-1, 1)
		}
		body = b.list.Render(w, listRows, st) + "\n" + st.Dim(strings.Repeat("─", w)) + "\n" + b.readerView(w, readerRows, st)
	default:
		lw := w * 2 / 5
		body = tui.Columns(b.list.Render(lw, room, st), b.readerView(w-lw-3, room, st), lw)
	}
	frame := strings.Join(append(head, body), "\n")
	return tui.Frame(frame, tui.Hints(b.hints(), w, st), h)
}

func (b browser) readerView(w, h int, st tui.Style) string {
	if b.err != "" {
		return st.Red(tui.Fit(b.err, w))
	}
	return b.reader.Render(w, h, st)
}

// runVerbText runs one rota verb in process, the way a typed command would,
// and returns its plain output. A failure is its first stderr line.
func runVerbText(c *Ctx, repoScoped bool, args ...string) (string, error) {
	if repoScoped && c.Repo != "" {
		args = append(args, "--repo", c.Repo)
	}
	var out, errb bytes.Buffer
	if code := run(Tree(), c.deps(), args, strings.NewReader(""), &out, &errb); code != ExitOK {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = "failed"
		}
		return "", errors.New(strutil.FirstLine(msg))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// dataObjs reads o[key] as a list of objects; anything else is skipped.
func dataObjs(data any, key string) []*jsonx.Object {
	o, _ := data.(*jsonx.Object)
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	list, _ := v.([]any)
	var out []*jsonx.Object
	for _, e := range list {
		if eo, ok := e.(*jsonx.Object); ok {
			out = append(out, eo)
		}
	}
	return out
}

func dataFloat(o *jsonx.Object, key string) (float64, bool) {
	v, ok := o.Get(key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	if i, ok := jsonx.Int(v); ok {
		return float64(i), true
	}
	return 0, false
}

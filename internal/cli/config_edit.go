package cli

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tui"
)

// `rota config edit` (#542) and `rota config show --ui` open the config
// screen (spec #538): keys grouped by section, a detail pane built from the
// schema metadata, and edits that go through the code `rota config set`
// runs. The screen is a tui.Model over rows read from `config show`'s Data;
// it writes through a configStore, which a test replaces with a fake.
//
// The numbered prompt loop `config edit` used to be is gone, not kept as a
// fallback: its type check was stricter than `config set`, and a terminal
// that cannot take raw mode is refused with a pointer to `config set`, like
// one that is not a terminal.

func configEdit(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "config edit takes no arguments"); err != nil {
			return Result{}, err
		}
		d := c.deps()
		if c.JSON || tui.Dumb() || !d.IsTerminal(c.Stdin) || !d.IsTerminal(c.Stdout) {
			return Result{}, Refused("config edit needs an interactive terminal").
				WithHint("run: rota config set <key> <value>")
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		store := rootStore{root}
		rows, err := store.Rows()
		if err != nil {
			return Result{}, err
		}
		var changed []string
		if err := d.RunView(c, newConfigScreen(rows, store, &changed)); err != nil {
			return Result{}, err
		}
		text := "no changes"
		if len(changed) > 0 {
			text = "changed: " + strings.Join(changed, ", ")
		}
		return Result{Data: jsonObj("changed", anySlice(changed)), Text: text}, nil
	}
}

// configView is `config show`'s --ui view: the screen over the entries of its
// own Data. Screen actions write to the project the verb read.
func configView(c *Ctx, res Result) (tui.Model, error) {
	root, err := backlogScope(c)
	if err != nil {
		return nil, err
	}
	rows, err := rowsFromData(res.Data)
	if err != nil {
		return nil, err
	}
	return newConfigScreen(rows, rootStore{root}, new([]string)), nil
}

// cfgRow is one key as the screen shows it, read from a `config show` entry.
type cfgRow struct {
	Key, Source, Type, Group, Desc string
	Value, Default                 any
	Choices                        []string
}

// rowsFromData reads the entries `config show` puts in Data.
func rowsFromData(data any) ([]cfgRow, error) {
	obj, _ := data.(*jsonx.Object)
	if obj == nil {
		return nil, errors.New("config view: no entries in the result")
	}
	list, _ := obj.Get("entries")
	entries, _ := list.([]any)
	rows := make([]cfgRow, 0, len(entries))
	for _, e := range entries {
		o, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		str := func(k string) string { v, _ := o.Get(k); s, _ := v.(string); return s }
		r := cfgRow{Key: str("key"), Source: str("source"), Type: str("type"), Group: str("group"), Desc: str("desc")}
		r.Value, _ = o.Get("value")
		r.Default, _ = o.Get("default")
		if cs, ok := o.Get("choices"); ok {
			l, _ := cs.([]any)
			for _, ch := range l {
				if s, ok := ch.(string); ok {
					r.Choices = append(r.Choices, s)
				}
			}
		}
		if r.Group == "" {
			r.Group, _, _ = strings.Cut(r.Key, ".")
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// configStore is what the screen reads and writes through.
type configStore interface {
	Rows() ([]cfgRow, error)
	// Set writes raw as `config set` does, to config.local.json when local.
	// changed is false when the file already held that value.
	Set(key, raw string, local bool) (changed bool, err error)
	// Reset puts key back to its schema default.
	Reset(key string) error
}

// rootStore is the store on a project's .rota/.
type rootStore struct{ root string }

func (s rootStore) Rows() ([]cfgRow, error) {
	entries, err := config.Show(s.root, "", false)
	if err != nil {
		return nil, err
	}
	rows, _ := showRows(entries)
	return rowsFromData(jsonObj("entries", rows))
}

func (s rootStore) Set(key, raw string, local bool) (bool, error) {
	layer := config.LayerProject
	if local {
		layer = config.LayerLocal
	}
	res, err := config.SetIn(s.root, key, raw, layer)
	return res.Changed, notObjectInternal(err)
}

// Reset drops the local override, then makes the project value the default: a
// required key is written with it (config check wants it present), any other
// key is removed.
func (s rootStore) Reset(key string) error {
	if _, err := config.Unset(s.root, key, config.LayerLocal); err != nil {
		return notObjectInternal(err)
	}
	for _, k := range config.Keys {
		if k.Name != key {
			continue
		}
		if k.Required {
			b, err := jsonx.MarshalCompact(config.Default(k))
			if err != nil {
				return err
			}
			_, err = config.Set(s.root, key, string(b))
			return notObjectInternal(err)
		}
		_, err := config.Unset(s.root, key, config.LayerProject)
		return notObjectInternal(err)
	}
	return config.Validate(key, "")
}

func notObjectInternal(err error) error {
	if errors.Is(err, config.ErrNotObject) || errors.Is(err, config.ErrLocalNotObject) {
		return &Error{Exit: ExitInternal, Message: err.Error()}
	}
	return err
}

// ---- the screen ----------------------------------------------------------------

const (
	cfgKeyW   = 36 // widest key before the value column; longer keys are cut
	cfgValueW = 14
)

// cfgLine is one line of the list: a group header or a key row.
type cfgLine struct {
	group string
	row   int // index into rows; -1 for a header
}

// cfgEdit is the edit in progress on one row.
type cfgEdit struct {
	field tui.Field
	row   int
	local bool
}

// cfgPending is what the running Exec leaves for its Done.
type cfgPending struct {
	rows []cfgRow
	msg  string
	key  string
	set  bool // a write that may have changed the file
}

type configScreen struct {
	rows      []cfgRow
	store     configStore
	changed   *[]string
	collapsed map[string]bool
	list      tui.List
	lines     []cfgLine
	detail    tui.Detail
	edit      *cfgEdit
	pend      *cfgPending
	msg       string
	isErr     bool
}

func newConfigScreen(rows []cfgRow, store configStore, changed *[]string) configScreen {
	m := configScreen{rows: rows, store: store, changed: changed, collapsed: map[string]bool{}}
	return m.rebuild()
}

// groups are the group names in first-seen order.
func (m configScreen) groups() []string {
	var out []string
	for _, r := range m.rows {
		if !slices.Contains(out, r.Group) {
			out = append(out, r.Group)
		}
	}
	return out
}

func (r cfgRow) differs() bool { return compact(r.Value) != compact(r.Default) }

func compact(v any) string {
	b, err := jsonx.MarshalCompact(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// rebuild lays the list out again: grouped, with collapsed groups folded, or
// flat while a filter is on.
func (m configScreen) rebuild() configScreen {
	var items []string
	m.lines = m.lines[:0:0]
	filtering := m.list.Filter != ""
	for _, g := range m.groups() {
		if !filtering {
			n, diff := 0, 0
			for _, r := range m.rows {
				if r.Group == g {
					n++
					if r.differs() {
						diff++
					}
				}
			}
			arrow := "▾"
			if m.collapsed[g] {
				arrow = "▸"
			}
			head := fmt.Sprintf("%s %s (%d)", arrow, g, n)
			if diff > 0 {
				head = fmt.Sprintf("%s %s (%d, %d changed)", arrow, g, n, diff)
			}
			items = append(items, head)
			m.lines = append(m.lines, cfgLine{g, -1})
			if m.collapsed[g] {
				continue
			}
		}
		for i, r := range m.rows {
			if r.Group != g {
				continue
			}
			items = append(items, "  "+tui.Pad(tui.Fit(r.Key, cfgKeyW), cfgKeyW)+" "+
				tui.Pad(tui.Fit(compact(r.Value), cfgValueW), cfgValueW)+" ["+r.Source+"]")
			m.lines = append(m.lines, cfgLine{g, i})
		}
	}
	lines := m.lines
	m.list.Items = items
	m.list.Mark = func(i int, text string, st tui.Style) string {
		switch {
		case lines[i].row < 0:
			return st.Bold(text)
		case m.rows[lines[i].row].differs():
			return st.Yellow(text)
		}
		return text
	}
	m.list.Sel = min(m.list.Sel, max(len(m.list.Matches())-1, 0))
	return m
}

// current is the line under the cursor.
func (m configScreen) current() (cfgLine, bool) {
	i, ok := m.list.Selected()
	if !ok || i >= len(m.lines) {
		return cfgLine{}, false
	}
	return m.lines[i], true
}

func (m configScreen) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.Done:
		return m.done(msg.Err), tui.Cmd{}
	case tui.Key:
		if m.edit != nil {
			return m.updateEdit(msg)
		}
		return m.updateList(msg)
	}
	return m, tui.Cmd{}
}

// done folds in the outcome of the write Exec ran.
func (m configScreen) done(err error) configScreen {
	p := m.pend
	m.pend = nil
	if err != nil {
		m.msg, m.isErr = err.Error(), true
		return m
	}
	if p == nil {
		return m
	}
	m.rows, m.msg, m.isErr = p.rows, p.msg, false
	if p.set && !slices.Contains(*m.changed, p.key) {
		*m.changed = append(*m.changed, p.key)
	}
	return m.rebuild()
}

func (m configScreen) updateList(k tui.Key) (tui.Model, tui.Cmd) {
	if !m.list.Filtering {
		m.msg, m.isErr = "", false
		line, onLine := m.current()
		switch {
		case k.Is('q'), k.Kind == tui.KeyEsc && m.list.Filter == "", k.Kind == tui.KeyCtrlC:
			return m, tui.Cmd{Quit: true}
		case k.Kind == tui.KeyEnter && onLine, k.Is(' ') && onLine && line.row < 0:
			if line.row < 0 {
				return m.toggleGroup(line.group, !m.collapsed[line.group]), tui.Cmd{}
			}
			return m.startEdit(line.row, false), tui.Cmd{}
		case k.Is('l') && onLine && line.row >= 0:
			return m.startEdit(line.row, true), tui.Cmd{}
		case k.Is('r') && onLine && line.row >= 0:
			return m.reset(line.row)
		case k.Kind == tui.KeyLeft && onLine && m.list.Filter == "":
			return m.toggleGroup(line.group, true), tui.Cmd{}
		case k.Kind == tui.KeyRight && onLine && m.list.Filter == "":
			return m.toggleGroup(line.group, false), tui.Cmd{}
		}
	}
	if k.Kind == tui.KeyPgUp || k.Kind == tui.KeyPgDn {
		m.detail, _ = m.detail.Update(k)
		return m, tui.Cmd{}
	}
	l, _ := m.list.Update(k)
	m.list = l
	m.detail.Top = 0
	return m.rebuild(), tui.Cmd{}
}

// toggleGroup folds or unfolds g and puts the cursor on its header.
func (m configScreen) toggleGroup(g string, fold bool) configScreen {
	next := map[string]bool{}
	for k, v := range m.collapsed {
		next[k] = v
	}
	next[g] = fold
	m.collapsed = next
	for i, l := range m.lines {
		if l.row < 0 && l.group == g {
			m.list.Sel = i
		}
	}
	return m.rebuild()
}

// startEdit opens the field that fits the row's type.
func (m configScreen) startEdit(row int, local bool) configScreen {
	r := m.rows[row]
	label := r.Key
	if local {
		label += " (local)"
	}
	var f tui.Field
	switch {
	case r.Type == string(config.TypeBool):
		on, _ := r.Value.(bool)
		f = tui.Toggle{Label: label, On: on}
	case len(r.Choices) > 0:
		cur, _ := r.Value.(string)
		f = tui.Choice{Label: label, Options: r.Choices, Sel: max(slices.Index(r.Choices, cur), 0)}
	default:
		text := compact(r.Value)
		// A string with a control character (an ESC from a hand-edited
		// config.json) is shown as its JSON escape, which Coerce reads back.
		if s, ok := r.Value.(string); ok && !strings.ContainsFunc(s, unicode.IsControl) {
			text = s
		}
		key := r.Key
		f = tui.NewTextInput(label, text, func(s string) error {
			err := config.Validate(key, s)
			if err == nil {
				return nil
			}
			return errors.New(strings.TrimPrefix(err.Error(), "invalid value for "+key+": "))
		})
	}
	m.edit = &cfgEdit{field: f, row: row, local: local}
	return m
}

func (m configScreen) updateEdit(k tui.Key) (tui.Model, tui.Cmd) {
	f, state := m.edit.field.Update(k)
	m.edit.field = f
	switch state {
	case tui.Cancelled:
		m.edit = nil
	case tui.Submitted:
		e := *m.edit
		m.edit = nil
		return m.write(e.row, f.Value(), e.local)
	}
	return m, tui.Cmd{}
}

// write sends raw to the store; the rows are read again once it is written.
func (m configScreen) write(row int, raw string, local bool) (tui.Model, tui.Cmd) {
	r, store := m.rows[row], m.store
	where := ".rota/config.json"
	if local {
		where = ".rota/config.local.json"
	}
	msg := fmt.Sprintf("%s = %s  (%s)", r.Key, compact(config.Coerce(raw)), where)
	if !local && r.Source == "local" {
		msg += "; config.local.json still overrides it here"
	}
	p := &cfgPending{key: r.Key, msg: msg}
	m.pend = p
	return m, tui.Cmd{Exec: func() error {
		changed, err := store.Set(r.Key, raw, local)
		if err != nil {
			return err
		}
		p.set = changed
		p.rows, err = store.Rows()
		return err
	}}
}

func (m configScreen) reset(row int) (tui.Model, tui.Cmd) {
	r, store := m.rows[row], m.store
	if r.Source == "default" || (r.Source == "project" && !r.differs()) {
		m.msg = r.Key + " is already the default"
		return m, tui.Cmd{}
	}
	p := &cfgPending{key: r.Key, msg: fmt.Sprintf("%s reset to %s", r.Key, compact(r.Default))}
	m.pend = p
	return m, tui.Cmd{Exec: func() error {
		if err := store.Reset(r.Key); err != nil {
			return err
		}
		p.set = true
		var err error
		p.rows, err = store.Rows()
		return err
	}}
}

func (m configScreen) Render(w, h int, st tui.Style) string {
	if h <= 0 {
		h = 30
	}
	lw := min(66, w*2/3)
	body := h - 2 // below it: the status or edit line, then the hints
	m.detail = m.describe()
	left := m.list.Render(lw, body, st)
	right := m.detail.Render(max(w-lw-3, 10), body, st)
	cols := strings.Split(tui.Columns(left, right, lw), "\n")
	for len(cols) < body {
		cols = append(cols, "")
	}
	status := m.statusLine(w, st)
	return tui.Frame(strings.Join(cols[:body], "\n")+"\n"+status, tui.Hints(m.hints(), w, st), h)
}

func (m configScreen) statusLine(w int, st tui.Style) string {
	switch {
	case m.edit != nil:
		return m.edit.field.Render(w, st)
	case m.list.Filtering:
		return tui.Fit(m.list.FilterLine(), w)
	case m.isErr:
		return st.Red(tui.Fit(m.msg, w))
	case m.msg != "":
		return st.Green(tui.Fit(m.msg, w))
	}
	if f := m.list.FilterLine(); f != "" {
		return tui.Fit(f, w)
	}
	return ""
}

func (m configScreen) hints() []tui.Hint {
	switch m.edit.kind() {
	case "toggle":
		return []tui.Hint{{Key: "space", Desc: "toggle"}, {Key: "enter", Desc: "save"}, {Key: "esc", Desc: "cancel"}}
	case "choice":
		return []tui.Hint{{Key: "←/→", Desc: "pick"}, {Key: "enter", Desc: "save"}, {Key: "esc", Desc: "cancel"}}
	case "text":
		return []tui.Hint{{Key: "enter", Desc: "save"}, {Key: "esc", Desc: "cancel"}}
	}
	return []tui.Hint{{Key: "↑/↓", Desc: "move"}, {Key: "enter", Desc: "edit"}, {Key: "l", Desc: "local"},
		{Key: "r", Desc: "reset"}, {Key: "/", Desc: "filter"}, {Key: "←/→", Desc: "fold"}, {Key: "q", Desc: "back"}}
}

func (e *cfgEdit) kind() string {
	if e == nil {
		return ""
	}
	switch e.field.(type) {
	case tui.Toggle:
		return "toggle"
	case tui.Choice:
		return "choice"
	}
	return "text"
}

// describe is the detail pane for the line under the cursor.
func (m configScreen) describe() tui.Detail {
	d := m.detail
	line, ok := m.current()
	if !ok {
		return tui.Detail{}
	}
	if line.row < 0 {
		n, diff := 0, 0
		for _, r := range m.rows {
			if r.Group == line.group {
				n++
				if r.differs() {
					diff++
				}
			}
		}
		d.Title = line.group
		d.Body = fmt.Sprintf("%d keys, %d differ from the default.\n\nEnter folds or unfolds the group.", n, diff)
		return d
	}
	r := m.rows[line.row]
	var b strings.Builder
	b.WriteString(r.Desc + "\n\n")
	fmt.Fprintf(&b, "Type: %s\nDefault: %s\nValue: %s\nSet in: %s", r.Type, compact(r.Default), compact(r.Value), sourceWhere(r.Source))
	if len(r.Choices) > 0 {
		b.WriteString("\nChoices: " + strings.Join(r.Choices, ", "))
	}
	d.Title, d.Body = r.Key, b.String()
	return d
}

func sourceWhere(src string) string {
	switch src {
	case "local":
		return ".rota/config.local.json"
	case "project":
		return ".rota/config.json"
	}
	return "nowhere, the default applies"
}

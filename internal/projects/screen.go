package projects

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/tui"
)

// Row is one project as the screen shows it. Status is "ok", "missing
// directory" or "no .rota/"; Round and Lease are what the detail pane says
// about the project's rounds, already phrased.
type Row struct {
	Name, Path, LastSeen string
	Status               string
	Round, Lease         string
}

// Screen is `rota projects --ui` (#538): the registry as a filterable list
// with a detail pane. It changes nothing itself; every action calls the verb
// the CLI exposes through Run, and Load re-reads the registry afterwards.
type Screen struct {
	// Load re-reads the rows after an action.
	Load func() ([]Row, error)
	// Run calls a rota verb in process on the cooked terminal: the arguments
	// as typed after `rota`.
	Run func(args ...string) error

	rows    []Row
	list    tui.List
	mode    mode
	confirm tui.Confirm
	input   tui.TextInput
	// target is the row a confirmed removal acts on, fixed when the confirm opens.
	target Row
	note   string
}

type mode int

const (
	browse mode = iota
	confirming
	prompting
)

// NewScreen shows rows; an empty registry still opens, with a hint to add one.
func NewScreen(rows []Row, load func() ([]Row, error), run func(args ...string) error) Screen {
	s := Screen{Load: load, Run: run}
	return s.withRows(rows)
}

func (s Screen) withRows(rows []Row) Screen {
	s.rows = rows
	items := make([]string, len(rows))
	for i, r := range rows {
		items[i] = r.Name + "  " + r.Status
	}
	f, sel := s.list.Filter, s.list.Sel
	s.list = tui.List{Items: items, Filter: f}
	if n := len(s.list.Matches()); sel < n {
		s.list.Sel = sel
	}
	return s
}

func (s Screen) selected() (Row, bool) {
	i, ok := s.list.Selected()
	if !ok {
		return Row{}, false
	}
	return s.rows[i], true
}

// reload re-reads the registry after an action; a failed read keeps the rows.
func (s Screen) reload() Screen {
	if s.Load == nil {
		return s
	}
	rows, err := s.Load()
	if err != nil {
		s.note = err.Error()
		return s
	}
	return s.withRows(rows)
}

func (s Screen) exec(wait bool, args ...string) tui.Cmd {
	run := s.Run
	return tui.Cmd{Exec: func() error { return run(args...) }, Cooked: true, Wait: wait}
}

// Update applies a key, or the Done of an action it started.
func (s Screen) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch m := msg.(type) {
	case tui.Done:
		s.note = ""
		if m.Err != nil {
			s.note = m.Err.Error()
		}
		return s.reload(), tui.Cmd{}
	case tui.Key:
		switch s.mode {
		case confirming:
			return s.updateConfirm(m)
		case prompting:
			return s.updatePrompt(m)
		}
		return s.updateBrowse(m)
	}
	return s, tui.Cmd{}
}

func (s Screen) updateBrowse(k tui.Key) (tui.Model, tui.Cmd) {
	if k.Kind == tui.KeyCtrlC {
		return s, tui.Cmd{Quit: true}
	}
	if s.list.Filtering || k.Kind == tui.KeyUp || k.Kind == tui.KeyDown {
		s.list, _ = s.list.Update(k)
		s.note = ""
		return s, tui.Cmd{}
	}
	row, has := s.selected()
	switch {
	case k.Kind == tui.KeyEsc && s.list.Filter == "", k.Is('q'):
		return s, tui.Cmd{Quit: true}
	case k.Kind == tui.KeyEnter:
		if has {
			return s, s.exec(false, "-C", row.Path)
		}
	case k.Is('c'):
		return s, s.exec(true, "projects", "cleanup")
	case k.Is('d'):
		if has {
			s.mode, s.target = confirming, row
			s.confirm = tui.Confirm{Prompt: "Remove " + row.Name + " from the registry? The directory stays."}
		}
	case k.Is('n'):
		s.mode = prompting
		s.input = tui.NewTextInput("Directory to init", "", func(v string) error {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("enter a directory")
			}
			if fi, err := os.Stat(expandHome(strings.TrimSpace(v))); err != nil || !fi.IsDir() {
				return fmt.Errorf("not a directory")
			}
			return nil
		})
	default:
		s.list, _ = s.list.Update(k)
	}
	return s, tui.Cmd{}
}

func (s Screen) updateConfirm(k tui.Key) (tui.Model, tui.Cmd) {
	c, st := s.confirm.Update(k)
	s.confirm = c
	if st == tui.Editing {
		return s, tui.Cmd{}
	}
	s.mode = browse
	if st == tui.Submitted && c.Yes {
		return s, s.exec(true, "projects", "remove", s.target.Path)
	}
	return s, tui.Cmd{}
}

func (s Screen) updatePrompt(k tui.Key) (tui.Model, tui.Cmd) {
	f, st := s.input.Update(k)
	s.input = f.(tui.TextInput)
	switch st {
	case tui.Cancelled:
		s.mode = browse
	case tui.Submitted:
		s.mode = browse
		dir, _ := filepath.Abs(expandHome(strings.TrimSpace(s.input.Text)))
		return s, s.exec(true, "-C", dir, "init")
	}
	return s, tui.Cmd{}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

// Render draws the list beside the detail pane, the prompt line if one is
// open, and the key hints.
func (s Screen) Render(w, h int, st tui.Style) string {
	lw := w * 2 / 5
	if w <= 0 {
		lw = 40
	}
	body := h - 3 // title, prompt line, hint bar
	left := s.list.Render(lw, body, st)
	if f := s.list.FilterLine(); f != "" {
		left += "\n" + tui.Fit(f, lw)
	}
	if len(s.rows) == 0 {
		left = st.Dim("  no projects registered: press n to add one")
	}
	var right string
	if r, ok := s.selected(); ok {
		right = detail(r, max(w-lw-3, 0), body, st)
	}
	top := st.Bold("Projects") + st.Dim(fmt.Sprintf("  %d registered", len(s.rows))) + "\n" + tui.Columns(left, right, lw)
	rows := strings.Split(top, "\n")
	if h > 0 {
		rows = rows[:min(len(rows), h-2)]
		for len(rows) < h-2 {
			rows = append(rows, "")
		}
	}
	return strings.Join(append(rows, s.prompt(w, st), s.bar(w, st)), "\n")
}

func detail(r Row, w, h int, st tui.Style) string {
	lastSeen := r.LastSeen
	if lastSeen == "" {
		lastSeen = "unknown"
	}
	body := strings.Join([]string{
		"path: " + r.Path,
		"status: " + r.Status,
		"last round: " + r.Round,
		"round lease: " + r.Lease,
		"last seen: " + lastSeen,
	}, "\n")
	return tui.Detail{Title: r.Name, Body: body}.Render(w, h, st)
}

func (s Screen) prompt(w int, st tui.Style) string {
	switch s.mode {
	case confirming:
		return s.confirm.Render(w, st)
	case prompting:
		return s.input.Render(w, st)
	}
	if s.note != "" {
		return st.Red(tui.Fit("error: "+s.note, w))
	}
	return ""
}

func (s Screen) bar(w int, st tui.Style) string {
	switch s.mode {
	case confirming:
		return tui.Hints([]tui.Hint{{Key: "y", Desc: "remove"}, {Key: "n/esc", Desc: "keep"}}, w, st)
	case prompting:
		return tui.Hints([]tui.Hint{{Key: "enter", Desc: "init"}, {Key: "esc", Desc: "cancel"}}, w, st)
	}
	return tui.Hints([]tui.Hint{
		{Key: "↑/↓", Desc: "move"}, {Key: "/", Desc: "filter"}, {Key: "enter", Desc: "open"},
		{Key: "c", Desc: "cleanup"}, {Key: "d", Desc: "remove"}, {Key: "n", Desc: "new"}, {Key: "q", Desc: "back"},
	}, w, st)
}

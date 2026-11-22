package plan

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
)

var (
	markerRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bTBD\b`),
		regexp.MustCompile(`(?i)\bunclear\b`),
		regexp.MustCompile(`(?i)\bunsure\b`),
		regexp.MustCompile(`(?i)\bopen questions?\b`),
		regexp.MustCompile(`(?i)\bheuristic TBD\b`),
	}
	codeSpanRe = regexp.MustCompile("`[^`]+`")
)

// OpenItem is what Uncertain reads of one open item: its ID and type as the
// backlog knows them, its backlog line, and for a Major item its detail text
// (HasDetail is false when there is none).
type OpenItem struct {
	ID, Type  string
	Major     bool
	Line      string
	Detail    string
	HasDetail bool
}

// Items finds open items. An item that does not exist or is not open is exit 3.
type Items interface {
	Open(id string) (OpenItem, error)
}

// Uncertain ports the retired uncertain helper: whether an open item warrants a peek before
// planning. Only Major items can be uncertain; the reasons are "no detail
// file", "multiple open-question signals" and "no concrete identifiers
// (unknown surface)". It returns the item's ID and type for the answer.
func Uncertain(src Items, id string) (itemID, typ string, reasons []string, err error) {
	it, err := src.Open(id)
	if err != nil {
		return "", "", nil, err
	}
	if !it.Major {
		return it.ID, it.Type, []string{}, nil
	}
	return it.ID, it.Type, uncertainReasons(it.Line, it.Detail, it.HasDetail), nil
}

// FileItems reads open items through be, the file backlog's Backend. openSections is
// the "Bugs|Features|Tasks" list that limits which open sections an item may
// live in; "" is that default.
func FileItems(be backlog.Backend, openSections string) Items { return fileItems{be, openSections} }

type fileItems struct {
	be           backlog.Backend
	openSections string
}

func (s fileItems) Open(id string) (OpenItem, error) {
	rows, err := s.be.Rows()
	if err != nil {
		if errors.Is(err, backlog.ErrNotFound) {
			return OpenItem{}, exitcode.Errf(exitcode.ExitResolution, ".rota/BACKLOG.md not found")
		}
		return OpenItem{}, err
	}
	// The sections list (ROTA_OPEN_SECTIONS, as the retired type registry exported it) limits
	// which open sections the item may live in.
	active := map[string]bool{}
	sections := s.openSections
	if sections == "" {
		sections = "Bugs|Features|Tasks"
	}
	for _, name := range strings.Split(sections, "|") {
		if name = strings.TrimSpace(name); name != "" {
			active[name] = true
		}
	}
	for _, r := range rows {
		if r.ID != id || !active[r.Section] {
			continue
		}
		it := OpenItem{ID: id, Type: r.Type, Line: r.Raw}
		if !strings.EqualFold(r.Tag, "major") {
			return it, nil
		}
		it.Major = true
		if it.Detail, it.HasDetail, err = s.be.Detail(id); err != nil {
			return OpenItem{}, err
		}
		return it, nil
	}
	return OpenItem{}, exitcode.Errf(exitcode.ExitResolution, "item %s not found in BACKLOG.md", id)
}

// NewIssueItems reads open items from the issue backend; open connects to it
// on first use, resolving the item.
func NewIssueItems(open func() (backlog.Backend, error)) Items { return issueItems{open} }

type issueItems struct {
	open func() (backlog.Backend, error)
}

// Open takes the item's issue body (without the fields block) as the detail.
// Only open items count, as only open bullets did.
func (s issueItems) Open(id string) (OpenItem, error) {
	be, err := s.open()
	if err != nil {
		return OpenItem{}, err
	}
	it, err := be.Get(id)
	if err != nil {
		return OpenItem{}, err
	}
	if it.Closed {
		return OpenItem{}, exitcode.Errf(exitcode.ExitResolution, "item %s is not open", it.ID)
	}
	out := OpenItem{ID: it.ID, Type: it.Type, Line: it.Line}
	if !strings.EqualFold(it.Tag, "major") {
		return out, nil
	}
	out.Major = true
	if out.Detail, out.HasDetail, err = be.Detail(it.ID); err != nil {
		return OpenItem{}, err
	}
	return out, nil
}

// uncertainReasons applies the three gates to a Major item's bullet and its
// detail text (has is false when there is none).
func uncertainReasons(line, detail string, has bool) []string {
	reasons := []string{}
	body := line
	if has {
		body += "\n" + detail
	} else {
		reasons = append(reasons, "no detail file")
	}
	marker := false
	for _, re := range markerRes {
		if re.MatchString(body) {
			marker = true
			break
		}
	}
	if strings.Count(body, "?") >= 2 || marker {
		reasons = append(reasons, "multiple open-question signals")
	}
	if !codeSpanRe.MatchString(body) {
		reasons = append(reasons, "no concrete identifiers (unknown surface)")
	}
	return reasons
}

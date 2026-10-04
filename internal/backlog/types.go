// Package backlog is the shared model of backlog items. It reads items from
// either backend (BACKLOG.md and its archive, or the issue tracker) and
// presents them as one Item, matching bin/hvlib_backend.py, hvlib_bullet.py
// and hvlib_types.py.
//
// The Python helpers match with regexps whose \s, \d and \w are Unicode. Go's
// are ASCII, so patterns without lookahead use pystr.SpaceClass and \p{Nd}
// instead, and the ones with lookahead are hand-written scanners that
// reproduce Python's lazy match plus lookahead exactly.
//
// To enumerate a backlog, call Backend.List: it returns Items with canonical
// IDs ("B07" in file mode, "12" in issue mode) whichever backend is open.
// Backend.Markdown renders BACKLOG.md-shaped text for renderers only; its
// bullets spell issue IDs differently from Item.ID, so do not parse them to
// find items.
package backlog

// Type is one row of the item-type registry (ROTA_TYPE_REGISTRY in bin/hv-types.sh).
type Type struct {
	Letter    string // "B"
	Section   string // open-section heading, "Bugs" ("" for S)
	Kind      string // detail directory and counters.json key, "bugs" ("" for S)
	Countable bool   // C flag: counted in counters.json since_refactor
	Plannable bool   // P flag: can carry a milestone plan key
}

// Types is the registry in bin/hv-types.sh order: B, F, T and the plan-key
// only S (Slice). A test keeps it in step with that file.
var Types = []Type{
	{"B", "Bugs", "bugs", true, true},
	{"F", "Features", "features", true, true},
	{"T", "Tasks", "tasks", false, true},
	{"S", "", "", false, true},
}

// ItemLetters are the letters of the types that appear as bullets in the open
// backlog sections (ITEM_TYPES).
const ItemLetters = "BFT"

// OpenSections are the "## " headings that hold open items, in registry order.
var OpenSections = []string{"Bugs", "Features", "Tasks"}

// TypeByLetter returns the registry row for a type letter.
func TypeByLetter(l string) (Type, bool) {
	for _, t := range Types {
		if t.Letter == l {
			return t, true
		}
	}
	return Type{}, false
}

// TypeByKind returns the row whose detail directory is kind ("bugs" gives B).
// Rows without a Kind (the Slice type) never match.
func TypeByKind(kind string) (Type, bool) {
	if kind == "" {
		return Type{}, false
	}
	for _, t := range Types {
		if t.Kind == kind {
			return t, true
		}
	}
	return Type{}, false
}

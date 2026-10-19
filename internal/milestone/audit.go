package milestone

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
)

// Findings is what a ship-time look at a milestone finds: the ids of its open
// child items and of its criteria still unticked. Both lists are non-nil.
type Findings struct {
	Open, Unticked []string
}

// Clean is true when nothing blocks shipping.
func (f Findings) Clean() bool { return len(f.Open)+len(f.Unticked) == 0 }

var unticked = regexp.MustCompile(`^\s*[-*]\s+\[ \]`)

// Audit holds a milestone body and the ids of its open items to being done.
// A criterion is unticked when its checkbox line is `[ ]`; one without an
// AC-n id is reported under the id Number would give it. Pure.
func Audit(body string, open []string) Findings {
	f := Findings{Open: append([]string{}, open...), Unticked: []string{}}
	labelled, _ := acceptance.Number(body)
	lines := strings.Split(labelled, "\n")
	for _, c := range acceptance.IDs(body) {
		if c.Line-1 < len(lines) && unticked.MatchString(lines[c.Line-1]) {
			f.Unticked = append(f.Unticked, c.ID)
		}
	}
	return f
}

package ship

import (
	"context"
	"regexp"
	"strconv"

	"github.com/l4ci/rota/internal/tracker"
)

// Forge is the part of the tracker adapter opening a PR needs.
type Forge interface {
	Provider() string
	PRNeedsBase() bool
	PRCreate(ctx context.Context, s tracker.PRSpec) (string, error)
}

// PRRequest is one PR to open. Body is the text as given; the Closes lines
// are appended to it.
type PRRequest struct {
	Branch, Title, Body string
	Items               []string
}

// PRPorts are what OpenPR cannot decide itself. Closes turns item IDs into
// the `Closes #n` lines (empty in file mode); Verdict is the B3 gate; Base
// resolves the base branch for a forge that needs one. Their errors pass
// through unchanged.
type PRPorts struct {
	Git     Git
	Forge   Forge
	Closes  func(ids []string) (string, error)
	Verdict func(branch string) error
	Base    func() (string, error)
}

// PR is the opened pull request. Number is 0 when the URL carries none.
type PR struct {
	URL, Provider string
	Number        int
}

var numberRe = regexp.MustCompile(`(\p{Nd}+)/?$`)

// OpenPR pushes the branch and opens the PR. The Closes lines and the verdict
// gate come before the push, so a bad item ID or a blocked branch leaves
// nothing pushed.
func OpenPR(ctx context.Context, p PRPorts, r PRRequest) (PR, error) {
	body := r.Body
	lines, err := p.Closes(r.Items)
	if err != nil {
		return PR{}, err
	}
	if lines != "" {
		body += "\n\n" + lines
	}
	if err := p.Verdict(r.Branch); err != nil {
		return PR{}, err
	}
	var base string
	if p.Forge.PRNeedsBase() {
		if base, err = p.Base(); err != nil {
			return PR{}, err
		}
	}
	push, err := p.Git.Run("push", "-u", "origin", r.Branch)
	if err != nil {
		return PR{}, err
	}
	if push.ExitCode != 0 {
		return PR{}, &GitError{Msg: "git push -u origin " + r.Branch + " failed: " + firstLine(push.Stderr)}
	}
	url, err := p.Forge.PRCreate(ctx, tracker.PRSpec{Title: r.Title, Body: body, Head: r.Branch, Base: base})
	if err != nil {
		return PR{}, err
	}
	pr := PR{URL: url, Provider: p.Forge.Provider()}
	if m := numberRe.FindStringSubmatch(url); m != nil {
		pr.Number, _ = strconv.Atoi(m[1])
	}
	return pr, nil
}

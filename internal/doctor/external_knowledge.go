package doctor

import (
	"strings"
)

// ExternalKnowledge is what was found at the knowledge.external path. Path is
// "" when the key is unset; the other fields are only read when it is set.
type ExternalKnowledge struct {
	Path      string
	Exists    bool // the path exists and is a directory
	IsGit     bool // it is a git work tree
	HasRemote bool // the clone has a remote to fetch from and push to
	HasPolicy bool // rota-knowledge.json is at its root
	// Over names each topic past a size limit, and the index past the topic
	// limit; empty when nothing is over or the clone could not be read.
	Over []string
	// Stray lists flat topic files an older rota left beside a split topic.
	Stray []string
	// Malformed lists index lines whose path is not a plain topic slug; rota
	// ignores them.
	Malformed []string
}

// externalKnowledge reports the state of the external knowledge clone: unset
// (skip), missing, not a git repo, no remote, no policy file (warn) or ok.
func (d *runner) externalKnowledge() Check {
	const name = "external knowledge"
	k := d.in.ExternalKnowledge
	switch {
	case k.Path == "":
		return skip(name, "knowledge.external is not set")
	case !k.Exists:
		return fail(name, k.Path+" does not exist", "clone the knowledge repo there, or run: rota config set knowledge.external <path> --global")
	case !k.IsGit:
		return fail(name, k.Path+" is not a git repository", "run git clone <url> "+k.Path+", or point knowledge.external at the clone")
	case !k.HasRemote:
		return fail(name, k.Path+" has no git remote", "run: git -C "+k.Path+" remote add origin <url>")
	case !k.HasPolicy:
		return Check{Name: name, Status: Warn, Detail: k.Path + " has no rota-knowledge.json: writes default to push", Hint: `add {"write":"push"} or {"write":"pr"} to rota-knowledge.json at the repo root`}
	}
	return pass(name, k.Path)
}

// externalKnowledgeSize warns when a topic or the index of the external
// knowledge base has outgrown its limits. A base within them adds no line.
func (d *runner) externalKnowledgeSize() (Check, bool) {
	over := d.in.ExternalKnowledge.Over
	if len(over) == 0 {
		return Check{}, false
	}
	return Check{Name: "external knowledge size", Status: Warn, Detail: strings.Join(over, "; "), Hint: "/rota-learn --external-review drafts a split; rota knowledge split --external applies it"}, true
}

// externalKnowledgeLayout warns when an older rota left a flat topic file
// beside a split topic's folder.
func (d *runner) externalKnowledgeLayout() (Check, bool) {
	stray := d.in.ExternalKnowledge.Stray
	if len(stray) == 0 {
		return Check{}, false
	}
	return Check{Name: "external knowledge layout", Status: Warn, Detail: strings.Join(stray, ", ") + " sits beside a split topic folder (an older rota wrote it)", Hint: "fold it in with: rota knowledge split --external --topic <topic>"}, true
}

// externalKnowledgeIndex warns about index lines that name a path outside the
// slugs rota writes (a glob, "..", capitals). They are ignored, never followed.
func (d *runner) externalKnowledgeIndex() (Check, bool) {
	bad := d.in.ExternalKnowledge.Malformed
	if len(bad) == 0 {
		return Check{}, false
	}
	return Check{Name: "external knowledge index", Status: Warn, Detail: strings.Join(bad, "; ") + " (ignored: not a plain topic slug)", Hint: "fix or remove the line in the knowledge repo"}, true
}

// externalKnowledgeChecks is the external-knowledge family: nothing while the
// path is unset, else the clone check and each finding after it.
func (d *runner) externalKnowledgeChecks() []Check {
	if d.in.ExternalKnowledge.Path == "" {
		return nil
	}
	checks := []Check{d.externalKnowledge()}
	for _, fn := range []func() (Check, bool){d.externalKnowledgeSize, d.externalKnowledgeLayout, d.externalKnowledgeIndex} {
		if c, ok := fn(); ok {
			checks = append(checks, c)
		}
	}
	return checks
}

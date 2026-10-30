package cycle

import (
	"path"
	"strings"
)

// Kind classifies a dirty path for the clean-tree sweep.
type Kind int

const (
	// User is any change that is not a sweepable sibling.
	User Kind = iota
	// ToolSibling is a file a toolchain regenerates beside the code.
	ToolSibling
	// Artifact is the target item's own design or plan file.
	Artifact
)

// Classify says what a dirty path is. tracked reports whether a path is under
// version control; itemIDs are the target items, matched exactly against
// .rota/designs/<ID>.md and .rota/plans/<ID>.md.
func Classify(p string, tracked func(string) bool, itemIDs []string) Kind {
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	base := path.Base(p)
	for _, id := range itemIDs {
		if p == ".rota/designs/"+id+".md" || p == ".rota/plans/"+id+".md" {
			return Artifact
		}
	}
	switch {
	case base == ".DS_Store", base == "Package.resolved":
		return ToolSibling
	case strings.HasSuffix(p, ".gd.uid"):
		return ToolSibling
	case path.Base(path.Dir(p)) != "." && strings.HasSuffix(path.Dir(p), ".xcworkspace") && base == "contents.xcworkspacedata":
		return ToolSibling
	case strings.HasSuffix(path.Dir(p), ".xcodeproj") && base == "project.pbxproj":
		return ToolSibling
	}
	// Beside a tracked file: Foo.gd.uid next to tracked Foo.gd.
	if ext := path.Ext(p); ext != "" && tracked != nil && tracked(strings.TrimSuffix(p, ext)) {
		return ToolSibling
	}
	return User
}

// IsSibling reports whether p may be swept without asking: a tool sibling or
// the target's own artifact.
func IsSibling(p string, tracked func(string) bool, itemIDs []string) bool {
	return Classify(p, tracked, itemIDs) != User
}

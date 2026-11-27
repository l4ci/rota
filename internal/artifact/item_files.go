package artifact

import (
	"os"
	"path"
	"strings"

	"github.com/l4ci/rota/internal/rotatree"
)

// ItemFileOwner returns the target item whose design or item plan p names,
// or "" for an unrelated path. Designs are <ID>.md; file plans qualify the
// item with a milestone (<milestone>-<ID>.md). Slice plans belong to no item.
// This identifies artifacts, without changing the plan store's key grammar.
func ItemFileOwner(p string, ids []string) string {
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	dir, name := path.Dir(p), path.Base(p)
	for _, id := range ids {
		if id == "" {
			continue
		}
		if (dir == rotatree.Rel(rotatree.DesignsDir) && name == id+".md") ||
			(dir == rotatree.Rel(rotatree.PlansDir) && strings.HasSuffix(name, "-"+id+".md")) {
			return id
		}
	}
	return ""
}

// ItemFiles lists an item's existing design and plan paths relative to root,
// in directory and filename order. Missing or unreadable directories have no
// entries. Directories named like documents are not notes.
func ItemFiles(root, id string) []string {
	files := []string{}
	for _, dir := range []string{rotatree.DesignsDir, rotatree.PlansDir} {
		entries, _ := os.ReadDir(rotatree.File(root, dir))
		for _, entry := range entries {
			p := rotatree.Rel(dir, entry.Name())
			if !entry.IsDir() && ItemFileOwner(p, []string{id}) != "" {
				files = append(files, p)
			}
		}
	}
	return files
}

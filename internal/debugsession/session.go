// Package debugsession keeps /rota-debug's working notes: one markdown file
// per session under .rota/debug/, per-developer and gitignored. The Iron Law
// counter keeps <branch>.json in the same directory; sessions are the .md files.
package debugsession

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// slugRe has no '/' and no '.', so a slug cannot climb out of .rota/debug/.
var slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func checkSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return exitcode.Errf(exitcode.ExitUsage, "slug must be letters, digits, '-' or '_', got '%s'", slug)
	}
	return nil
}

func file(root, slug string) string { return docs(root).Path(slug) }

// Put writes the session file, replacing any earlier one.
func Put(root, slug, body string) error {
	if err := checkSlug(slug); err != nil {
		return err
	}
	path := file(root, slug)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	if err := selfIgnore(dir); err != nil {
		return err
	}
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		return fsio.WriteFileAtomic(path, []byte(body))
	})
}

// selfIgnore writes dir/.gitignore ("*") when missing, so a session never shows
// in git status, whether or not the project's root .gitignore has the rota
// block yet. An existing file is left alone.
func selfIgnore(dir string) error {
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return fsio.WriteFileAtomic(gi, []byte("*\n"))
}

// docs is the session store; FileDocs owns the not-found wording and locking.
func docs(root string) artifact.FileDocs {
	return artifact.FileDocs{Root: root, Dir: rotatree.DebugDir, Kind: "debug session"}
}

// Show returns the session file's text.
func Show(root, slug string) (string, error) {
	if err := checkSlug(slug); err != nil {
		return "", err
	}
	return docs(root).Read(slug)
}

// List returns the open session slugs, sorted. A missing directory is none.
func List(root string) ([]string, error) {
	ents, err := os.ReadDir(rotatree.File(root, rotatree.DebugDir))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	slugs := []string{}
	for _, e := range ents {
		if slug, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() && slugRe.MatchString(slug) {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	return slugs, nil
}

// Rm deletes the session file.
func Rm(root, slug string) error {
	if err := checkSlug(slug); err != nil {
		return err
	}
	return docs(root).Remove(slug)
}

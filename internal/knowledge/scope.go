// Package knowledge ports the KNOWLEDGE.md helpers (hv-knowledge-*): topic
// reads, bullet writes, the tier sidecar and the contradiction queue. File
// formats match bin/hvlib_knowledge.py byte for byte.
package knowledge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// Umbrella is the scope name of the project-level files.
const Umbrella = "umbrella"

// ErrScope is wrapped by every scope-resolution failure, so callers can map
// it to the resolution exit code.
var ErrScope = errors.New("scope")

// Store locates the knowledge files of one project. Root is the project root
// (the directory holding .rota/); Repos maps registered sub-repo names to their
// absolute paths and is empty outside umbrella mode.
type Store struct {
	Root  string
	Repos map[string]string
}

// check validates a sub-repo scope against the registry.
func (s Store) check(scope string) error {
	if scope == "" || scope == Umbrella {
		return nil
	}
	if len(s.Repos) == 0 {
		return fmt.Errorf("%w: sub-repo scope '%s' requested but umbrella mode is off (no sub-repos registered); run rota init from the umbrella root", ErrScope, scope)
	}
	if _, ok := s.Repos[scope]; !ok {
		names := make([]string, 0, len(s.Repos))
		for n := range s.Repos {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("%w: sub-repo '%s' not registered in .rota/repos.json; registered: %s", ErrScope, scope, strings.Join(names, ", "))
	}
	return nil
}

// dir is the directory holding the scope's knowledge files.
func (s Store) dir(scope string) string {
	if scope == "" || scope == Umbrella {
		return rotatree.Dir(s.Root)
	}
	return rotatree.File(s.Root, rotatree.KnowledgeDir, scope)
}

// KnowledgePath is the KNOWLEDGE.md of scope. The directory is not created:
// writers create it when they write.
func (s Store) KnowledgePath(scope string) (string, error) {
	if err := s.check(scope); err != nil {
		return "", err
	}
	return filepath.Join(s.dir(scope), "KNOWLEDGE.md"), nil
}

// TierPath is the knowledge-tier.json sidecar of scope.
func (s Store) TierPath(scope string) (string, error) {
	if err := s.check(scope); err != nil {
		return "", err
	}
	return filepath.Join(s.dir(scope), "knowledge-tier.json"), nil
}

// ReadFile returns the file's text with newlines normalized (fsio.ReadText),
// or "" when it does not exist.
func ReadFile(path string) (string, error) {
	t, err := fsio.ReadText(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return t, err
}

// writeText writes text atomically, creating the parent directory first.
func writeText(path, text string, write func(string, []byte) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return write(path, []byte(text))
}

// readTextBytes is fsio.ReadText for callers that work on bytes.
func readTextBytes(path string) ([]byte, error) {
	t, err := fsio.ReadText(path)
	return []byte(t), err
}

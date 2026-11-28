package repos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// gitDirs returns a root holding .rota/ and a directory with a .git entry per name.
func gitDirs(t *testing.T, names ...string) string {
	t.Helper()
	root := Realpath(t.TempDir())
	if err := os.Mkdir(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// together runs n calls released at once and returns their errors by index.
func together(n int, call func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = call(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

func names(root string) []string {
	var out []string
	for _, r := range Load(root) {
		out = append(out, r.Name)
	}
	return out
}

// succeededOnce checks one call won and every other one failed with want.
func succeededOnce(t *testing.T, errs []error, want error) {
	t.Helper()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, want):
			t.Errorf("error = %v, want %v", err, want)
		}
	}
	if won != 1 {
		t.Errorf("%d calls succeeded, want 1", won)
	}
}

func TestConcurrentAdd(t *testing.T) {
	var dirs []string
	for i := range 12 {
		dirs = append(dirs, fmt.Sprintf("r%02d", i))
	}
	add := func(root, dir, name string) error {
		_, _, err := Add(root, filepath.Join(root, dir), name)
		return err
	}

	t.Run("different repos all persist", func(t *testing.T) {
		root := gitDirs(t, dirs...)
		for i, err := range together(len(dirs), func(i int) error { return add(root, dirs[i], "") }) {
			if err != nil {
				t.Errorf("add %s: %v", dirs[i], err)
			}
		}
		if got := names(root); !reflect.DeepEqual(got, dirs) {
			t.Errorf("registered %v, want %v", got, dirs)
		}
	})
	t.Run("one name for many directories", func(t *testing.T) {
		root := gitDirs(t, dirs...)
		succeededOnce(t, together(len(dirs), func(i int) error { return add(root, dirs[i], "web") }), ErrConflict)
	})
	t.Run("many names for one directory", func(t *testing.T) {
		root := gitDirs(t, "web")
		succeededOnce(t, together(len(dirs), func(i int) error { return add(root, "web", dirs[i]) }), ErrConflict)
		if got := names(root); len(got) != 1 {
			t.Errorf("registered %v, want one name", got)
		}
	})
}

func TestConcurrentRemove(t *testing.T) {
	root := gitDirs(t, "web")
	if _, _, err := Add(root, filepath.Join(root, "web"), ""); err != nil {
		t.Fatal(err)
	}
	succeededOnce(t, together(12, func(int) error {
		_, err := Remove(root, "web")
		return err
	}), ErrUnknown)
}

// A registry that is not a JSON object is refused and left as it is.
func TestEditKeepsCorruptRegistry(t *testing.T) {
	for _, body := range []string{"not json", "[]"} {
		root := gitDirs(t, "web")
		reg := filepath.Join(root, ".rota", "repos.json")
		if err := os.WriteFile(reg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Add(root, filepath.Join(root, "web"), ""); err == nil {
			t.Errorf("%q: Add succeeded", body)
		}
		if got, _ := os.ReadFile(reg); string(got) != body {
			t.Errorf("%q: registry became %q", body, got)
		}
		if err := SetTopLevel(root, []string{"web"}); err != nil {
			t.Errorf("%q: SetTopLevel: %v", body, err)
		}
		if got := names(root); !reflect.DeepEqual(got, []string{"web"}) {
			t.Errorf("%q: after SetTopLevel %v", body, got)
		}
	}
}

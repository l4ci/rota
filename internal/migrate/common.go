// Package migrate holds the one-shot project migrations: `rota migrate hv`
// moves a project from the hv era to rota. This file keeps the pieces it shares:
// refusals, the installed-version seam, small JSON getters, file copy and the
// unified diff.
package migrate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// Sentinels the verb maps to exit codes.
var (
	// ErrRefused: a safety precondition does not hold (exit 4).
	ErrRefused = errors.New("refused")
	// ErrGit: git is missing or the project is not a git repository (exit 5).
	ErrGit = errors.New("git")
)

// word or phrase, for the verb's failure data.
type Refusal struct{ Blocked, Message string }

func (r *Refusal) Error() string { return r.Message }

// Unwrap lets errors.Is(err, ErrRefused) match.
func (r *Refusal) Unwrap() error { return ErrRefused }

func refuse(blocked, format string, a ...any) error {
	return &Refusal{blocked, fmt.Sprintf(format, a...)}
}

// InstalledVersion is the version stamped into rota.version; "" skips the

// InstalledVersion is the version stamped into rota.version; "" skips the
// stamp. It is the running binary's version, and tests replace it.
var InstalledVersion = func() string { return "" }

func getObj(o *jsonx.Object, k string) (*jsonx.Object, bool) {
	v, _ := o.Get(k)
	r, ok := v.(*jsonx.Object)
	return r, ok
}

func getString(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

// stampVersion writes rota.version and drops the legacy forms: hv.version and

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

// UnifiedDiff renders a unified diff with three lines of context in the
// layout of difflib.unified_diff. A rewrite replaces tokens inside lines and
// never adds or removes a line, so the two texts line up one to one.
func UnifiedDiff(old, new, label string) string {
	a, b := splitLines(old), splitLines(new)
	if len(a) != len(b) {
		return fmt.Sprintf("--- %s\n+++ %s (rewritten)\n", label, label)
	}
	const ctx = 3
	var changed []int
	for i := range a {
		if a[i] != b[i] {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s (rewritten)\n", label, label)
	// Group changed lines into hunks: two changes belong together when no more
	// than 2*ctx unchanged lines separate them.
	i := 0
	for i < len(changed) {
		j := i
		for j+1 < len(changed) && changed[j+1]-changed[j]-1 <= 2*ctx {
			j++
		}
		start, stop := max(changed[i]-ctx, 0), min(changed[j]+1+ctx, len(a))
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", diffRange(start, stop), diffRange(start, stop))
		for k := start; k < stop; {
			if a[k] == b[k] {
				out.WriteString(" " + a[k])
				k++
				continue
			}
			end := k
			for end < stop && a[end] != b[end] {
				end++
			}
			for x := k; x < end; x++ {
				out.WriteString("-" + a[x])
			}
			for x := k; x < end; x++ {
				out.WriteString("+" + b[x])
			}
			k = end
		}
		i = j + 1
	}
	return out.String()
}

func diffRange(start, stop int) string {
	begin, length := start+1, stop-start
	if length == 1 {
		return fmt.Sprint(begin)
	}
	if length == 0 {
		begin--
	}
	return fmt.Sprintf("%d,%d", begin, length)
}

func splitLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

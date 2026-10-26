package backlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/section"
)

// PostReport upserts a rendered report on one item's backend.
// The first line is the report marker; a repeated kind and SHA replaces the
// earlier report instead of adding another record.
func PostReport(be Backend, ref, markdown string) error {
	key, _, _ := strings.Cut(markdown, "\n")
	if !strings.HasPrefix(key, marker.Prefix+marker.KindReport+" ") || !strings.HasSuffix(key, " -->") {
		return errf(ErrInvalid, "report must start with a rota report marker")
	}
	p, ok := be.(interface {
		postReport(string, string, string) error
	})
	if !ok {
		return ErrWrongBackend
	}
	return p.postReport(ref, key, strings.TrimRight(markdown, "\n")+"\n")
}

func (f *File) postReport(ref, key, markdown string) error {
	dir := detailDir(ref)
	if dir == "" {
		return errf(ErrNotFound, "[%s] has no detail directory (expected B/F/T prefix)", ref)
	}
	_, title, ok := FindOrigin(f.Corpus(), ref)
	if !ok {
		return errf(ErrNotFound, "[%s] not found in BACKLOG.md or ARCHIVE.md", ref)
	}
	if title == "" {
		title = ref
	}
	path := f.rota(dir, ref+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		content, err := fsio.ReadText(path)
		if errors.Is(err, os.ErrNotExist) {
			content = "# " + ref + ": " + title + "\n\n> Related TODO entry: `[" + ref + "]` in `.rota/BACKLOG.md`\n"
		} else if err != nil {
			return err
		}
		body := section.Body(content, "Reports")
		start := -1
		if strings.HasPrefix(body, key+"\n") {
			start = 0
		} else if at := strings.Index(body, "\n"+key+"\n"); at >= 0 {
			start = at + 1
		}
		if start >= 0 {
			end := len(body)
			if at := strings.Index(body[start+len(key):], "\n"+marker.Prefix+marker.KindReport+" "); at >= 0 {
				end = start + len(key) + at + 1
			}
			body = body[:start] + markdown + "\n" + body[end:]
		} else {
			body = strings.TrimRight(body, "\n")
			if body == "" {
				body = "\n" + markdown + "\n"
			} else {
				body += "\n\n" + markdown + "\n"
			}
		}
		out := section.Replace(content, "Reports", body)
		if out == content {
			return nil
		}
		return fsio.WriteFileAtomic(path, []byte(out))
	})
}

func (b *Issues) postReport(ref, key, markdown string) error {
	n, err := b.number(ref)
	if err != nil {
		return err
	}
	tr, err := b.tracker()
	if err != nil {
		return err
	}
	comments, err := tr.Comments(b.ctx(), n)
	if err != nil {
		return err
	}
	for _, c := range comments {
		line, _, _ := strings.Cut(c.Body, "\n")
		if line == key {
			if c.Body == markdown {
				return nil
			}
			return tr.EditComment(b.ctx(), n, c.ID, markdown)
		}
	}
	_, err = tr.AddComment(b.ctx(), n, markdown)
	return err
}

func (u *Umbrella) postReport(ref, key, markdown string) error {
	return u.onOwner(ref, func(s *Issues, plain string) error { return s.postReport(plain, key, markdown) })
}

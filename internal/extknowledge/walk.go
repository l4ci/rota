package extknowledge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// topicFileRef is one topic file of the base, read for a walk.
type topicFileRef struct {
	Topic   Topic
	Rel     string // repo-relative, slash-separated
	Content string
	Stray   bool // a flat file beside a split topic: read, never written
}

// bullets lists the bullets of the file.
func (f topicFileRef) bullets() []bullet { return f.Topic.bullets(f.Content) }

// heading names the file's section in query and match output.
func (f topicFileRef) heading() string {
	if f.Stray {
		return f.Topic.Name + " (stray: " + f.Rel + ")"
	}
	return f.Topic.Name
}

// topicWalk is what a walk selected.
type topicWalk struct {
	Files   []topicFileRef // each file once
	Missing []string       // requests that named no index entry, each once
	Strays  []StrayRef
}

// walk is the one traversal of the base's topics. An empty want selects every
// topic and subtopic in index order; otherwise the topics each request names,
// in request order, with their subtopics when subtopics is set. A split
// topic's stray flat file follows it. A file indexed but not written yet is
// skipped; any other unreadable file, stray or not, is an error.
func (c Clone) walk(want []string, subtopics bool) (topicWalk, error) {
	topics, err := c.Topics()
	if err != nil {
		return topicWalk{}, err
	}
	var w topicWalk
	seen := map[string]bool{}
	read := func(t Topic, stray bool) error {
		if seen[t.File] {
			return nil
		}
		seen[t.File] = true
		raw, err := c.readRegular(filepath.Join(c.Dir, filepath.FromSlash(t.File)))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		w.Files = append(w.Files, topicFileRef{Topic: t, Rel: t.File, Content: string(raw), Stray: stray})
		return nil
	}
	visit := func(t Topic) error {
		if err := read(t, false); err != nil {
			return err
		}
		if t.Stray == "" || seen[t.Stray] {
			return nil
		}
		w.Strays = append(w.Strays, StrayRef{Topic: t.Name, File: t.Stray})
		st := t
		st.File, st.Split, st.Stray = t.Stray, false, ""
		return read(st, true)
	}
	if len(want) == 0 {
		for _, t := range topics {
			if err := visit(t); err != nil {
				return topicWalk{}, err
			}
		}
		return w, nil
	}
	missed := map[string]bool{}
	for _, name := range want {
		hit := resolve(name, topics)
		if len(hit) == 0 {
			if k := strings.ToLower(strings.TrimSpace(name)); k != "" && !missed[k] {
				missed[k] = true
				w.Missing = append(w.Missing, name)
			}
			continue
		}
		for _, t := range hit {
			if err := visit(t); err != nil {
				return topicWalk{}, err
			}
			if !subtopics {
				continue
			}
			for _, st := range t.Subtopics {
				if err := visit(st); err != nil {
					return topicWalk{}, err
				}
			}
		}
	}
	return w, nil
}

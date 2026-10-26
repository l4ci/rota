package extknowledge

import (
	"fmt"
	"path/filepath"
	"strings"
)

// TopicStat is the size of one topic file.
type TopicStat struct {
	Name    string
	File    string // repo-relative, slash-separated
	Bullets int
	Bytes   int
	Hits    int    // hits on this machine; only StatsWithHits fills it
	Parent  string // the split topic a subtopic belongs to
	Stray   bool   // a flat file an older rota left beside a split topic
}

// Stats measures every indexed topic whose file exists, in index order. It
// reads the clone as it is: no fetch.
func (c Clone) Stats() ([]TopicStat, error) {
	files, err := c.topicFiles()
	if err != nil {
		return nil, err
	}
	out := make([]TopicStat, 0, len(files))
	for _, f := range files {
		out = append(out, TopicStat{Name: f.Topic.Name, File: filepath.ToSlash(f.Rel), Bullets: len(f.bullets()), Bytes: len(f.Content), Parent: f.Topic.Parent, Stray: f.Stray})
	}
	return out, nil
}

// Limits are the size thresholds of the base; 0 disables one.
type Limits struct{ Bullets, Bytes, Topics int }

// Over names every topic past a bullet or byte limit, once per topic with each
// breach in it, and the index when it lists more topics than the limit, in
// index order. A subtopic and a stray file count as topics of their own but
// not toward the index limit.
func (l Limits) Over(stats []TopicStat) []string {
	var out []string
	indexed := 0
	for _, s := range stats {
		if s.Parent == "" && !s.Stray {
			indexed++
		}
		var parts []string
		if l.Bullets > 0 && s.Bullets > l.Bullets {
			parts = append(parts, fmt.Sprintf("%d bullets > %d", s.Bullets, l.Bullets))
		}
		if l.Bytes > 0 && s.Bytes > l.Bytes {
			parts = append(parts, fmt.Sprintf("%d bytes > %d", s.Bytes, l.Bytes))
		}
		if len(parts) > 0 {
			out = append(out, fmt.Sprintf("%s (%s)", s.Name, strings.Join(parts, ", ")))
		}
	}
	if l.Topics > 0 && indexed > l.Topics {
		out = append(out, fmt.Sprintf("index has %d topics (limit %d)", indexed, l.Topics))
	}
	return out
}

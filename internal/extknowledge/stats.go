package extknowledge

import (
	"fmt"
	"path/filepath"
)

// TopicStat is the size of one topic file.
type TopicStat struct {
	Name    string
	File    string // repo-relative, slash-separated
	Bullets int
	Bytes   int
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
		out = append(out, TopicStat{Name: f.Topic.Name, File: filepath.ToSlash(f.Rel), Bullets: len(parseBullets(f.Content)), Bytes: len(f.Content)})
	}
	return out, nil
}

// Limits are the size thresholds of the base; 0 disables one.
type Limits struct{ Bullets, Bytes, Topics int }

// Over names every topic past a bullet or byte limit and the index when it
// lists more topics than the limit, in index order.
func (l Limits) Over(stats []TopicStat) []string {
	var out []string
	for _, s := range stats {
		if l.Bullets > 0 && s.Bullets > l.Bullets {
			out = append(out, fmt.Sprintf("%s (%d bullets, limit %d)", s.Name, s.Bullets, l.Bullets))
		}
		if l.Bytes > 0 && s.Bytes > l.Bytes {
			out = append(out, fmt.Sprintf("%s (%d bytes, limit %d)", s.Name, s.Bytes, l.Bytes))
		}
	}
	if l.Topics > 0 && len(stats) > l.Topics {
		out = append(out, fmt.Sprintf("index has %d topics (limit %d)", len(stats), l.Topics))
	}
	return out
}

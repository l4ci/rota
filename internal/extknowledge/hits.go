package extknowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hitsPath is the per-machine, per-clone log of consulted bullets. It lives in
// the state dir, never in the clone, so hits are not synced.
func (c Clone) hitsPath() string { return c.statePath(".hits.jsonl") }

// hitRow is one line of the hits log.
type hitRow struct {
	Topic   string `json:"topic"`
	Title   string `json:"title"`
	Date    string `json:"date"`
	Project string `json:"project"`
}

// BulletStat is one bullet's size-independent usage: its stamp date, the hits
// recorded on this machine and the date of the latest.
type BulletStat struct {
	Title   string
	Date    string // the stamp's date; empty when the bullet has none
	Hits    int
	LastHit string
}

// TopicHits is a topic's stat with the hits on its bullets.
type TopicHits struct {
	TopicStat
	Items []BulletStat
}

// readHits loads the log. A line that does not parse, or lacks a topic or
// title, is torn (a crashed write) and counted instead of read. A missing log
// is no hits.
func (c Clone) readHits() (rows []hitRow, torn int, err error) {
	p := c.hitsPath()
	if p == "" {
		return nil, 0, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var r hitRow
		if json.Unmarshal([]byte(l), &r) != nil || r.Topic == "" || r.Title == "" {
			torn++
			continue
		}
		rows = append(rows, r)
	}
	return rows, torn, nil
}

// hitKey joins a hit to a bullet by the topic family (a split topic and its
// subtopics are one) and the case-insensitive title, so a bullet that split
// moved keeps the hits recorded under its old topic.
func hitKey(family, title string) string {
	return strings.ToLower(family) + "\x00" + strings.ToLower(strings.TrimSpace(title))
}

// familyOf names the family of a topic name in a log row: the split topic for
// a known subtopic, else the part before ": " (a subtopic that was removed),
// else the name itself.
func familyOf(name string, topics []Topic) string {
	for _, t := range topics {
		if strings.EqualFold(t.Name, name) {
			if t.Parent != "" {
				return t.Parent
			}
			return t.Name
		}
	}
	if head, _, ok := strings.Cut(name, ": "); ok {
		return head
	}
	return name
}

func family(t Topic) string {
	if t.Parent != "" {
		return t.Parent
	}
	return t.Name
}

type hitTally struct {
	n    int
	last string
}

func (c Clone) tally(topics []Topic) (map[string]hitTally, int, error) {
	rows, torn, err := c.readHits()
	if err != nil {
		return nil, 0, err
	}
	m := map[string]hitTally{}
	for _, r := range rows {
		k := hitKey(familyOf(r.Topic, topics), r.Title)
		t := m[k]
		t.n++
		if r.Date > t.last {
			t.last = r.Date
		}
		m[k] = t
	}
	return m, torn, nil
}

// Hit records that an agent used the bullet titled title under topic, and
// returns the hits that bullet now has on this machine. It reads the clone as
// it is and appends one line to the hits log: no fetch, no write to the clone.
// ErrNotFound when the topic or the bullet is not in the base.
func (c Clone) Hit(topic, title, project string) (int, error) {
	p := c.hitsPath()
	if p == "" {
		return 0, fmt.Errorf("no state directory to keep hits in")
	}
	t, raw, err := c.topicFile(topic)
	if err != nil {
		return 0, err
	}
	want := strings.ToLower(strings.TrimSpace(title))
	found := false
	if raw != nil {
		for _, b := range t.bullets(string(raw)) {
			if b.Title != "" && strings.ToLower(b.Title) == want {
				title, found = b.Title, true
				break
			}
		}
	}
	if !found {
		return 0, fmt.Errorf("%w: no bullet in %q is titled %q", ErrNotFound, topic, title)
	}
	topics, err := c.Topics()
	if err != nil {
		return 0, err
	}
	line, err := json.Marshal(hitRow{Topic: t.Name, Title: title, Date: c.now().Format("2006-01-02"), Project: project})
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return 0, err
	}
	// One O_APPEND write per hit: concurrent rota processes interleave whole
	// lines, never characters.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return 0, werr
	}
	m, _, err := c.tally(topics)
	if err != nil {
		return 0, err
	}
	return m[hitKey(family(t), title)].n, nil
}

// StatsWithHits is Stats plus, per topic and bullet, the hits on this machine.
// torn counts log lines that could not be read.
func (c Clone) StatsWithHits() (out []TopicHits, torn int, err error) {
	files, err := c.topicFiles()
	if err != nil {
		return nil, 0, err
	}
	topics, err := c.Topics()
	if err != nil {
		return nil, 0, err
	}
	m, torn, err := c.tally(topics)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range files {
		bs := f.bullets()
		h := TopicHits{TopicStat: TopicStat{Name: f.Topic.Name, File: filepath.ToSlash(f.Rel), Bullets: len(bs), Bytes: len(f.Content), Parent: f.Topic.Parent, Stray: f.Stray}}
		for _, b := range bs {
			t := m[hitKey(family(f.Topic), b.Title)]
			if b.Title == "" {
				t = hitTally{}
			}
			h.Hits += t.n
			h.Items = append(h.Items, BulletStat{Title: b.Title, Date: b.Date, Hits: t.n, LastHit: t.last})
		}
		out = append(out, h)
	}
	return out, torn, nil
}

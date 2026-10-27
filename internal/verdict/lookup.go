package verdict

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
)

var reportMarker = regexp.MustCompile(`^<!-- rota:report ([a-z-]+) sha=([0-9a-f]{4,64}) -->$`)

// ParseReport reads the report format emitted by RenderReport. Gate reports,
// malformed markers and invalid bodies are not verdict records.
func ParseReport(text string) (Record, bool) {
	line, _, _ := strings.Cut(text, "\n")
	m := reportMarker.FindStringSubmatch(line)
	if m == nil || !slices.Contains(BranchKinds, m[1]) {
		return Record{}, false
	}
	at := strings.LastIndex(text, "\n### Verdict body\n\n")
	if at < 0 {
		return Record{}, false
	}
	block := text[at+len("\n### Verdict body\n\n"):]
	opening, raw, ok := strings.Cut(block, "\n")
	fence := strings.TrimSuffix(opening, "json")
	if !ok || opening == fence || len(fence) < 3 || strings.Trim(fence, "`") != "" {
		return Record{}, false
	}
	raw, ok = strings.CutSuffix(strings.TrimSpace(raw), "\n"+fence)
	if !ok {
		return Record{}, false
	}
	body, err := ParseBody(raw)
	accepted, _ := Takes(m[1], body.Verdict)
	if err != nil || body.Verdict == "" || !accepted {
		return Record{}, false
	}
	header := "\n### Verdict report\n\nKind: `" + m[1] + "`\n\nVerdict: **" + body.Verdict + "**\n\nReviewed SHA: `" + m[2] + "`\n"
	if !strings.HasPrefix(text, line+header) {
		return Record{}, false
	}
	return Record{Kind: m[1], Verdict: body.Verdict, Sha: m[2], Summary: body.Summary, Findings: body.Findings, Items: body.Items, Declined: body.Declined}, true
}

// ReportSource is the forge surface needed to recover a verdict. Authentication
// and optional repository write access are resolved through the same adapter.
type ReportSource interface {
	MRNotes(context.Context, int) ([]tracker.Comment, error)
}

// RecoverPR fills missing kind/head entries from trusted PR reports. Local
// records at head win. Only current-head reports are cached, never stale ones.
func RecoverPR(ctx context.Context, root, key, head string, pr int, source ReportSource) error {
	if head == "" {
		return nil
	}
	notes, err := source.MRNotes(ctx, pr)
	if err != nil {
		return err
	}
	candidates := map[string]Record{}
	trusted := map[string]bool{}
	var self string
	for _, note := range notes {
		r, ok := ParseReport(note.Body)
		if !ok || !AtHead(r, head) {
			continue
		}
		if note.Author == "" {
			continue
		}
		allowed, known := trusted[note.Author]
		if !known {
			if self == "" {
				self, err = tracker.ReportAuthor(ctx, source)
				if err != nil {
					return err
				}
			}
			allowed = note.Author == self
			if !allowed {
				if access, ok := source.(interface {
					CanPush(context.Context, string) (bool, error)
				}); ok {
					allowed, err = access.CanPush(ctx, note.Author)
					if err != nil {
						return err
					}
				}
			}
			trusted[note.Author] = allowed
		}
		if !allowed {
			continue
		}
		r.FromReport = true
		r.RecordedAt = note.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		candidates[r.Kind] = r // notes are oldest first; the newest valid report wins
	}
	if len(candidates) == 0 {
		return nil
	}
	return update(root, func(s *Store) {
		list := s.Branches[key]
		// Stable kind order keeps spec before quality for the effective verdict.
		for _, kind := range BranchKinds {
			r, ok := candidates[kind]
			if !ok {
				continue
			}
			if local, ok := Latest(list, kind); ok && AtHead(local, head) {
				continue
			}
			if kind == ReviewSpec {
				r.Combined = r.Verdict
			}
			if kind == ReviewQuality {
				r.Combined = r.Verdict
				if spec, ok := Latest(list, ReviewSpec); ok && AtHead(spec, head) {
					r.Combined = Worst(spec.Verdict, r.Verdict)
				}
			}
			list = appendCapped(list, r)
		}
		s.Branches[key] = list
	})
}

// ForHead keeps all local verdicts and only the recovered reports at head.
// Local stale FAILs retain their existing policy; a cached PR report cannot
// acquire that authority merely because another clone downloaded it.
func ForHead(list []Record, head string) []Record {
	out := make([]Record, 0, len(list))
	for _, r := range list {
		if !r.FromReport || AtHead(r, head) {
			out = append(out, r)
		}
	}
	return out
}

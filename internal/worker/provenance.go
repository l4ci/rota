package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/tracker"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/jsonx"
)

// Approval provenance: the writer side (the relay log a dispatch appends to)
// and the reader side (the `## Approvals` section of a PR body, cross-checked
// against that log) live here, so the format has one owner. Nothing in this
// file touches git or a forge; the gate hands it text and a relay list.

// newRelayEntry is one relays[] record: {round, ts, summary}.
func newRelayEntry(round int, now time.Time, brief string) *jsonx.Object {
	entry := jsonx.NewObject()
	entry.Set("round", round)
	entry.Set("ts", stamp.Format(now))
	entry.Set("summary", relaySummary(brief))
	return entry
}

// LogRelay appends a relays[] record for a brief delivered outside a relay
// dispatch (a transfer's hand-off), so the gate finds it when a PR cites it.
func LogRelay(root, slot string, round int, now time.Time, brief string) error {
	entry := newRelayEntry(round, now, brief)
	_, err := UpdateSlot(root, slot, func(s *Slot) { s.AppendRelay(entry) })
	return err
}

// relaySummary is the first non-blank line of the brief that is not the
// signature, stripped and cut to 200 characters.
func relaySummary(text string) string {
	for _, l := range pystr.Splitlines(text) {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "--- ORCHESTRATOR") {
			if utf8.RuneCountInString(l) > 200 {
				l = string([]rune(l)[:200])
			}
			return l
		}
	}
	return ""
}

var (
	reApprovalsHead = regexp.MustCompile(`(?i)^##\s+Approvals\s*$`)
	reH2            = regexp.MustCompile(`^##(\s|$)`)
	reSpaces        = regexp.MustCompile(`\s+`)
	reOfferCite     = regexp.MustCompile(`support offer`)
	reRelayCite     = regexp.MustCompile(`orchestrator relay(?:\s+round\s+(\d+))?`)
)

func norm(s string) string {
	return strings.TrimSpace(reSpaces.ReplaceAllString(strings.ToLower(s), " "))
}

// RelaysFromComments turns the relay comments of an issue into relays[]
// records ({round, summary}) for CheckApprovals. Only comments whose author
// passes trust.Writer count: anyone who can comment could otherwise forge the
// orchestrator's word. A failed trust lookup drops the comment.
func RelaysFromComments(ctx context.Context, trust *MarkerTrust, comments []tracker.Comment) []any {
	var out []any
	for _, c := range comments {
		round, text, ok := marker.ParseRelay(c.Body)
		if !ok || !trusted(trust, c.Author) {
			continue
		}
		e := jsonx.NewObject()
		e.Set("round", round)
		e.Set("summary", relaySummary(text))
		out = append(out, e)
	}
	return out
}

// HasSupportOffer reports whether a trusted author left the support-offer
// comment `round support offer` posts.
func HasSupportOffer(trust *MarkerTrust, comments []tracker.Comment) bool {
	for _, c := range comments {
		if marker.IsSupportOffer(c.Body) && trusted(trust, c.Author) {
			return true
		}
	}
	return false
}

func trusted(trust *MarkerTrust, author string) bool {
	ok, err := trust.Writer(author)
	return err == nil && ok
}

// CheckApprovalsOffer is CheckApprovals with the issue's offer known: a line
// citing the round support offer passes only when offered is true.
func CheckApprovalsOffer(body string, relayLog []any, offered bool) string {
	return checkApprovals(body, relayLog, true, offered)
}

// CheckApprovals cross-checks a PR body's `## Approvals` section against the
// slot's relay log. It returns "" for a pass, else what is wrong: a missing
// section while relays exist, a cited relay round that was never sent, or the
// maintainer cited for text the orchestrator relayed.
func CheckApprovals(body string, relayLog []any) string {
	return checkApprovals(body, relayLog, false, false)
}

// checkApprovals enforces the offer citation only when offerRequired: a PR
// that is not a support PR may mention a support offer freely (#1869).
func checkApprovals(body string, relayLog []any, offerRequired, offered bool) string {
	var relays []*jsonx.Object
	for _, r := range relayLog {
		if o, ok := r.(*jsonx.Object); ok {
			relays = append(relays, o)
		}
	}
	section, found := approvalsSection(body)
	if !found {
		if len(relays) > 0 {
			return fmt.Sprintf("%d relay(s) logged but the PR body has no ## Approvals section", len(relays))
		}
		return ""
	}
	rounds := map[int]bool{}
	for _, r := range relays {
		rv, _ := r.Get("round")
		if i, ok := jsonx.Int(rv); ok {
			rounds[i] = true
		}
	}
	var problems []string
	for _, line := range pystr.Splitlines(section) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		low := norm(line)
		if offerRequired && reOfferCite.MatchString(low) {
			if !offered {
				problems = append(problems, fmt.Sprintf("cites the round support offer but no trusted support-offer comment is on the issue: %s", strings.TrimSpace(line)))
			}
		}
		if m := reRelayCite.FindStringSubmatch(low); m != nil {
			if n := m[1]; n != "" {
				i, _ := strconv.Atoi(n)
				if !rounds[i] {
					problems = append(problems, fmt.Sprintf("cites an orchestrator relay for round %s but none is logged: %s", n, strings.TrimSpace(line)))
				}
			} else if len(relays) == 0 {
				problems = append(problems, fmt.Sprintf("cites an orchestrator relay but none is logged: %s", strings.TrimSpace(line)))
			}
		} else if strings.Contains(low, "maintainer") {
			for _, r := range relays {
				summary := norm(jsonx.Str(r, "summary"))
				if len([]rune(summary)) >= 12 && (strings.Contains(low, summary) || strings.Contains(summary, low)) {
					rv, _ := r.Get("round")
					problems = append(problems, fmt.Sprintf("cites the maintainer for text the orchestrator relayed (round %v): %s", rv, strings.TrimSpace(line)))
					break
				}
			}
		}
	}
	return strings.Join(problems, "; ")
}

// approvalsSection is the body of the `## Approvals` section: the lines after
// the heading up to the next `## ` heading.
func approvalsSection(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if !reApprovalsHead.MatchString(l) {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if reH2.MatchString(lines[j]) {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), true
	}
	return "", false
}

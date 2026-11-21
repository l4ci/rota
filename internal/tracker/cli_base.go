package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/strutil"
)

// base holds what both adapters share.
type base struct {
	cli          *CLI
	closing      func(string) []int
	labelEntries []labels.Entry
}

func (b *base) Provider() string { return b.cli.Provider }

// CheckAuth runs `auth status`; any failure, a missing CLI included, is
// KindUnavailable.
func (b *base) CheckAuth(ctx context.Context) error { return b.cli.CheckAuth(ctx) }

func (b *base) ClosedNumbers(body string) []int { return b.closing(body) }

// ClosesIssue: GitHub and GitLab close an issue through a closing keyword in
// the PR body, and name issues by number only.
func (b *base) ClosesIssue(key IssueKey, c ClosingSource) bool {
	n, ok := key.Number()
	return ok && slices.Contains(b.closing(c.Body), n)
}

// run makes one call; stdin carries body only when an argument is exactly "-".
// A failed call is KindNotFound when the CLI says the object does not exist.
func (b *base) run(ctx context.Context, args []string, body string) (string, error) {
	var stdin io.Reader
	for _, a := range args {
		if a == "-" {
			stdin = strings.NewReader(body)
			break
		}
	}
	res, err := b.cli.Run(ctx, args, stdin)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", FailedCall(string(res.Stderr), res.ExitCode)
	}
	return string(res.Stdout), nil
}

// FailedCall is a forge call that exited non-zero, as a tracker failure:
// KindNotFound when the CLI says the object does not exist, else KindFailed.
func FailedCall(stderr string, code int) *Error {
	kind := KindFailed
	if stderrIs(causeNotFound, []byte(stderr)) {
		kind = KindNotFound
	}
	return &Error{Kind: kind, Code: code, Message: strings.TrimSpace(stderr)}
}

// list runs a list command with an explicit page size and keeps fetching
// while pages come back full, so a long list is never cut short. more turns
// the page size and the 1-based page number into the paging arguments; when
// it returns grow, the page size doubles and the whole list is re-fetched
// instead (gh pages internally up to --limit).
func (b *base) list(ctx context.Context, args []string, size int, more func(size, page int) (extra []string, grow bool), v any) error {
	var all []json.RawMessage
	for page := 1; ; page++ {
		extra, grow := more(size, page)
		var rows []json.RawMessage
		if err := b.json(ctx, append(append([]string(nil), args...), extra...), &rows); err != nil {
			return err
		}
		if grow {
			all = rows
			if len(rows) < size {
				break
			}
			size *= 2
			continue
		}
		all = append(all, rows...)
		if len(rows) < size {
			break
		}
	}
	joined, _ := json.Marshal(all)
	return json.Unmarshal(joined, v)
}

func (b *base) json(ctx context.Context, args []string, v any) error {
	out, err := b.run(ctx, args, "")
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
	}
	return nil
}

// pages decodes an `api` list call whose paginated output concatenates one
// JSON array per page.
func (b *base) pages(ctx context.Context, path string, v any) error {
	out, err := b.run(ctx, []string{"api", path}, "")
	if err != nil {
		return err
	}
	var all []json.RawMessage
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(out)))
	for dec.More() {
		var page []json.RawMessage
		if err := dec.Decode(&page); err != nil {
			return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
		}
		all = append(all, page...)
	}
	joined, _ := json.Marshal(all)
	if err := json.Unmarshal(joined, v); err != nil {
		return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
	}
	return nil
}

func (b *base) createdID(ctx context.Context, args []string) (string, error) {
	var d struct{ ID json.RawMessage }
	if err := b.json(ctx, args, &d); err != nil {
		return "", err
	}
	n, ok := intOf(d.ID)
	if !ok {
		return "", failed("cannot parse comment id from: %q", strutil.Clip(string(d.ID), clipLen))
	}
	return strconv.Itoa(n), nil
}

// intOf reads a JSON number, or a string holding a decimal integer, as int
// (Python's int(v)).
func intOf(raw json.RawMessage) (int, bool) {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		i, err := strconv.Atoi(string(n))
		return i, err == nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		i, err := strconv.Atoi(strings.TrimSpace(s))
		return i, err == nil
	}
	return 0, false
}

// idText renders a comment id as hvlib_tracker._int_or_raw would: an integer
// when it reads as one, else the raw string.
func idText(raw json.RawMessage) string {
	if n, ok := intOf(raw); ok {
		return strconv.Itoa(n)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func (b *base) prsClosing(ctx context.Context, a Adapter, number int) ([]PR, error) {
	prs, err := a.OpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	var out []PR
	for _, p := range prs {
		for _, n := range a.ClosedNumbers(p.Body) {
			if n == number {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}

// numberFromURL reads the issue number off the last line of a create call.
func numberFromURL(out string) (int, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimRightFunc(lines[len(lines)-1], pystr.IsSpace)
	i := len(last)
	for i > 0 && last[i-1] >= '0' && last[i-1] <= '9' {
		i--
	}
	if i == len(last) || i == 0 || last[i-1] != '/' {
		return 0, failed("cannot parse issue number from: %q", strutil.Clip(strings.TrimSpace(out), clipLen))
	}
	n, err := strconv.Atoi(last[i:])
	if err != nil {
		return 0, failed("cannot parse issue number from: %q", strutil.Clip(strings.TrimSpace(out), clipLen))
	}
	return n, nil
}

type rawMilestone struct {
	Number      int    `json:"number"`
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       any    `json:"state"`
}

func (r rawMilestone) norm(number int) Milestone {
	m := Milestone{Number: number, Title: r.Title, Description: r.Description, State: "open"}
	if strings.ToLower(fmt.Sprint(r.State)) == "closed" {
		m.State = "closed"
	}
	return m
}

func (b *base) addLabels(ctx context.Context, a Adapter, key IssueKey, labels []string, autoCreate bool) error {
	return editLabels(ctx, func(ctx context.Context, k IssueKey, e IssueEdit) error {
		if err := a.EnsureLabels(ctx, e.AddLabels, autoCreate); err != nil {
			return err
		}
		return a.Edit(ctx, k, e)
	}, key, IssueEdit{AddLabels: labels, NoCreateLabels: !autoCreate})
}

func (b *base) removeLabels(ctx context.Context, a Adapter, key IssueKey, labels []string) error {
	return editLabels(ctx, a.Edit, key, IssueEdit{RemoveLabels: labels})
}

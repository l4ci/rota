package host

// Send path: brief size, busy check and input readiness.
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/harness"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (o *orca) Send(ctx context.Context, slot, handle, file string) error {
	text, err := o.d.readBrief(file)
	if err != nil {
		return err
	}
	return classifySend(o.send(ctx, handle, text))
}

const orcaBriefLimit = 120 * 1024

func orcaCheckSize(text string) error {
	if len(text) > orcaBriefLimit {
		return fmt.Errorf("Orca brief is %d UTF-8 bytes; limit is %d bytes; shorten the brief before resending", len(text), orcaBriefLimit)
	}
	return nil
}

func (o *orca) send(ctx context.Context, handle, text string) error {
	if err := orcaCheckSize(text); err != nil {
		return err
	}
	h, err := o.decodeOrca(handle)
	if err != nil {
		return err
	}
	if h.Branch == "" {
		return fmt.Errorf("cannot submit Orca prompt: saved worktree branch is missing")
	}
	before, err := o.readPane(ctx, &h, 0)
	handle = encodeOrca(h)
	if err != nil {
		return err
	}
	// Orca has no validated key-only transport. Refuse every dialog without
	// changing its selection or pasting text into it.
	if dialog, ok := orcaDialog(before); ok {
		return &DialogError{First: dialog.First}
	}
	readErr := error(nil)
	latest := before
	read := func() string {
		p, e := o.readPane(ctx, &h, 0)
		if e != nil {
			readErr = e
		}
		latest = p
		return stripDim(p)
	}
	if waitNoDraft(&o.d, read, "") != "" {
		return ErrDraftOnPrompt
	}
	if readErr != nil {
		return readErr
	}
	if dialog, ok := orcaDialog(latest); ok {
		return &DialogError{First: dialog.First}
	}
	before = latest
	handle = encodeOrca(h)
	if !orcaReady(before) && !o.inputReady(h, before) {
		return fmt.Errorf("orca input is not ready; wait for the agent prompt in the Orca terminal, then retry")
	}
	raw, err := o.call(ctx, "terminal", "send", "--terminal", h.ID, "--text", text, "--enter", "--wait-submit", "20", "--json")
	if err != nil {
		return err
	}
	started, err := orcaTurnStarted(raw)
	if err != nil {
		return err
	}
	if started {
		return o.verifyBranch(ctx, h)
	}
	return confirmPickup(&o.d, Orca, func(time.Duration) (bool, error) {
		after, e := o.ReadPane(ctx, "", handle, 0)
		if e != nil {
			return false, e
		}
		if after == before || humanDraft(stripDim(after), "") != "" {
			return false, nil
		}
		above := after
		if n := strings.LastIndexAny(after, promptMarkers); n >= 0 {
			above = after[:n]
		}
		// Draft-only changes are not submission. Require new output above the
		// composer containing explicit turn/busy evidence. An echo alone
		// proves input acceptance, not the turn-start required by dispatch.
		oldAbove := before
		if n := strings.LastIndexAny(before, promptMarkers); n >= 0 {
			oldAbove = before[:n]
		}
		if above == oldAbove {
			return false, nil
		}
		if orcaBusy.MatchString(above) && !orcaBusy.MatchString(oldAbove) && !orcaBusy.MatchString(text) {
			return true, o.verifyBranch(ctx, h)
		}
		return false, nil
	}, nil)
}

var orcaBusy = regexp.MustCompile(`(?im)^[ \t]*(?:[•●✶✻✽✢*][ \t]*)?[^\r\n]*\besc to interrupt\b[^\r\n]*$`)

func (o *orca) SendPane(ctx context.Context, pane, text string) error {
	if pane == "" {
		return fmt.Errorf("no Orca pane to send to")
	}
	if e := orcaCheckSize(text); e != nil {
		return e
	}
	if strings.HasPrefix(pane, "orca:") {
		h, e := o.decodeOrca(pane)
		if e != nil {
			return e
		}
		pane = h.ID
	}
	if pane == "" {
		return fmt.Errorf("no native Orca pane handle")
	}
	_, e := o.call(ctx, "terminal", "send", "--terminal", pane, "--text", text, "--enter", "--json")
	return e
}

// Once the startup banner scrolls away, a persisted successful boot and the
// still-live exec PID identify this as the same agent rather than a bare shell.
func (o *orca) inputReady(h orcaHandle, p string) bool {
	if _, ok := orcaDialog(p); ok {
		return false
	}
	for _, syntax := range harness.InputSyntaxes() {
		if syntax.ReadyModel != nil {
			if m := syntax.ReadyModel.FindStringSubmatch(p); len(m) > 1 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(m[1])), "loading") {
				return false
			}
		}
	}
	if _, e := o.d.FS.Stat(h.PIDFile + ".ready"); e != nil {
		return false
	}
	b, e := o.d.FS.ReadFile(h.PIDFile)
	if e != nil {
		return false
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	if e != nil || pid <= 0 || !o.d.Alive(pid) {
		return false
	}
	return strings.ContainsAny(p, promptMarkers) || orcaBusy.MatchString(p)
}

func orcaTurnStarted(raw string) (bool, error) {
	type receipt struct {
		Stages      []string `json:"stages"`
		Provider    string   `json:"provider"`
		Observation string   `json:"observation"`
	}
	var r receipt
	if e := json.Unmarshal([]byte(raw), &r); e != nil {
		return false, fmt.Errorf("orca send returned malformed JSON; pickup unconfirmed: %w", e)
	}
	if r.Stages == nil {
		var w struct {
			Receipt *receipt `json:"receipt"`
		}
		if json.Unmarshal([]byte(raw), &w) == nil && w.Receipt != nil {
			r = *w.Receipt
		}
	}
	if r.Observation != "" && r.Observation != "supported" {
		return false, nil
	}
	for _, stage := range r.Stages {
		if stage == "turn_started" {
			return true, nil
		}
	}
	return false, nil
}

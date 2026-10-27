package backlog

import (
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/roundcfg"
)

const claimLabelPrefix = labels.ClaimPrefix

// IsClaimLabel reports whether label is a visible, derived claim marker.
// It recognizes the prefix only; it does not validate the agent or machine.
func IsClaimLabel(label string) bool {
	return strings.HasPrefix(label, claimLabelPrefix)
}

// HasClaimLabel reports whether labels contains a claim marker.
func HasClaimLabel(labels []string) bool {
	return slices.ContainsFunc(labels, IsClaimLabel)
}

var claimLabelID = regexp.MustCompile(`^([a-z][a-z0-9-]*)@[0-9]+$`)

// ClaimLabel maps a round claim to its agent and machine. Support slots carry
// the committed round.support prefix; all other slots belong to main. Legacy
// claim ids and labels exceeding the tracker's 50-character limit are skipped.
func ClaimLabel(cfg any, claimID string) (string, bool) {
	m := claimLabelID.FindStringSubmatch(claimID)
	if m == nil {
		return "", false
	}
	agent, machine := m[1], "main"
	if support := roundcfg.SupportName(cfg); support != "" && strings.HasPrefix(agent, support+"-") {
		agent, machine = strings.TrimPrefix(agent, support+"-"), support
		if !claimLabelID.MatchString(agent + "@0") {
			return "", false
		}
	}
	label := claimLabelPrefix + agent + "@" + machine
	if len(label) > 50 {
		return "", false
	}
	return label, true
}

// ClaimLabelDrift compares labels with open claims, never the worker registry.
// Closed issues should carry no claim labels, even if old comment claims remain.
func (b *Issues) ClaimLabelDrift(ref string) (add, remove []string, err error) {
	if _, err = b.tracker(); err != nil {
		return
	}
	is, _, err := b.require(ref)
	if err != nil {
		return nil, nil, err
	}
	want := map[string]bool{}
	if is.State == "open" {
		held, err := b.heldClaims(is.Key)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range held {
			if label, ok := ClaimLabel(b.Cfg, id); ok {
				want[label] = true
			}
		}
	}
	for label := range want {
		if !slices.Contains(is.Labels, label) {
			add = append(add, label)
		}
	}
	for _, label := range is.Labels {
		if IsClaimLabel(label) && !want[label] {
			remove = append(remove, label)
		}
	}
	slices.Sort(add)
	slices.Sort(remove)
	return add, remove, nil
}

// SyncClaimLabels re-reads the claims and repairs their labels. Errors are
// returned so reconcile can leave drift visible and report a failed repair.
func (b *Issues) SyncClaimLabels(ref string) (changed bool, err error) {
	add, remove, err := b.ClaimLabelDrift(ref)
	if err != nil || len(add)+len(remove) == 0 {
		return false, err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return false, err
	}
	if len(add) > 0 {
		if err := b.Tracker.AddLabels(b.ctx(), is.Key, add, b.AutoCreate()); err != nil {
			return false, err
		}
		changed = true
	}
	if len(remove) > 0 {
		if err := b.Tracker.RemoveLabels(b.ctx(), is.Key, remove); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

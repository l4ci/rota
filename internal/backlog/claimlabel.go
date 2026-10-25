package backlog

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
)

// ClaimLabelPrefix identifies the visible, derived marker of a comment claim.
const ClaimLabelPrefix = "claimed:"

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
	label := ClaimLabelPrefix + agent + "@" + machine
	if len(label) > 50 {
		return "", false
	}
	return label, true
}

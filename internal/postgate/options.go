package postgate

import (
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/landing"
)

type GateResult = landing.GateResult
type TrainResult = landing.TrainResult

// GateTarget is the identity bookkeeping needs after execution.
type GateTarget struct{ Name, Branch, PR, Issue string }

// Approval checks the merge policy using lazily resolved files and thread.
type Approval interface {
	Check(string, func() ([]string, error), func() (landing.ApprovalThread, error)) (*exitcode.Refusal, error)
}

// GateOpts selects a gate operation; execution-only inputs stay with the runner.
type GateOpts struct {
	NoWait                                 bool
	Slot, Base                             string
	CheckOnly, NoVerify, FinalSlice, Prune bool
	Round                                  *landing.RoundMemo
	Approval                               Approval
	VerdictRepo                            string
	CheckReviews                           bool
	ItemBody                               func(string) (string, bool, error)
}

// TrainOpts selects a train operation and its input adapters.
type TrainOpts struct {
	NoWait                       bool
	Targets                      []string
	Round                        *landing.RoundMemo
	Base, SyncBase               string
	LandGreen                    bool
	OnFail                       string
	NoVerify                     bool
	FinalSlice                   []string
	Approve                      func(func() ([]string, error)) (*exitcode.Refusal, error)
	VerdictRepo                  string
	CheckReviews                 bool
	Order                        []string
	Say                          func(string)
	Plan                         bool
	ItemBody                     func(string) (string, bool, error)
	Integration, Onto, RequestID string
}

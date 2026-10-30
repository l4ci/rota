package land

import (
	"fmt"
	"strings"
)

// The subjects of the merge commits a landing writes. Producers format them
// here and `ship undo` recognises them here, so the two cannot drift.
const (
	mergePrefix = "merge: "
	trainPrefix = "train: "
	syncPrefix  = "sync "
)

// MergeSubject is the subject of a gate merge of branch into base.
func MergeSubject(branch, base string) string {
	return fmt.Sprintf("%s%s into %s", mergePrefix, branch, base)
}

// TrainSubject is the subject of one member's merge inside a train.
func TrainSubject(branch, base string) string {
	return fmt.Sprintf("%s%s into %s", trainPrefix, branch, base)
}

// SyncSubject is the subject of a merge of a sync base into a milestone branch.
func SyncSubject(syncBase, base string) string {
	return fmt.Sprintf("%s%s into %s", syncPrefix, syncBase, base)
}

// IsCycleMerge reports whether subject is a gate merge, the only landing
// `ship undo` rolls back. Train and sync merges are refused on purpose: a train
// lands several members through one push and a sync merges a shared base, so
// resetting past one of them would undo part of a landing others build on.
func IsCycleMerge(subject string) bool {
	return strings.HasPrefix(subject, mergePrefix)
}

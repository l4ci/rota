package worker

import "strings"

// ParkPrefix is the namespace of the branch a worktree rests on between
// issues. It is the one definition of the park-branch rule: build the name
// with ParkBranch, test with IsPark or IsOwnPark, never spell the literal.
const ParkPrefix = "park/"

// ParkBranch is the park branch of agent name.
func ParkBranch(name string) string { return ParkPrefix + name }

// IsPark reports whether branch is any agent's park branch.
func IsPark(branch string) bool { return strings.HasPrefix(branch, ParkPrefix) }

// IsOwnPark reports whether branch is agent name's own park branch.
func IsOwnPark(branch, name string) bool { return branch == ParkBranch(name) }

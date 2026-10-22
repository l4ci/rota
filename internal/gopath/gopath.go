// Package gopath decides which Go paths are test files and which are
// production code, for every rule that cares (the TDD warning, the stale-binary
// check, the review-depth declared set).
package gopath

import (
	"slices"
	"strings"
)

// IsTestGo reports whether f is a Go test file (*_test.go).
func IsTestGo(f string) bool { return strings.HasSuffix(f, "_test.go") }

// IsProdGo reports whether f is a Go file a test does not own: not *_test.go
// and not under a testdata directory. Non-Go paths are not Go at all.
func IsProdGo(f string) bool {
	return strings.HasSuffix(f, ".go") && !IsTestGo(f) &&
		!slices.Contains(strings.Split(f, "/"), "testdata")
}

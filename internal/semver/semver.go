// Package semver is the one place rota parses and orders release versions.
// Callers pick a rule by name: lenient never rejects, Strict accepts only a
// three-part core. The rules differ on purpose; a version string orders the
// same under one rule wherever it is compared.
package semver

import (
	"math/big"
	"regexp"
	"strings"
)

// Version is the numeric X.Y.Z core of a release version. Pre-release and
// build suffixes do not take part in ordering.
type Version struct{ parts [3]*big.Int }

var digits = regexp.MustCompile(`[0-9]+`)

// lenient reads the first three digit runs of s as major, minor and patch,
// zero padded, so it never fails: "dev" is 0.0.0, "2" is 2.0.0 and
// "v1.2.3-rc1" is 1.2.3. It is for comparing a stamped binary against a
// published release, where either side may be a build label.
func lenient(s string) Version {
	var v Version
	runs := digits.FindAllString(s, 3)
	for i := range v.parts {
		v.parts[i] = new(big.Int)
		if i < len(runs) {
			v.parts[i].SetString(runs[i], 10)
		}
	}
	return v
}

// Strict reads an optional leading "v", exactly three dot-separated decimal
// parts and an optional -pre or +build suffix; anything else is not ok. It is
// for gates where a malformed stamp must be told apart from an old one.
func Strict(s string) (Version, bool) {
	var v Version
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	fields := strings.Split(s, ".")
	if len(fields) != 3 {
		return v, false
	}
	for i, f := range fields {
		if f == "" || strings.Trim(f, "0123456789") != "" {
			return v, false
		}
		v.parts[i], _ = new(big.Int).SetString(f, 10)
	}
	return v, true
}

// Parts returns the major, minor and patch numbers.
func (v Version) Parts() [3]*big.Int { return v.parts }

// Compare returns -1, 0 or 1 as v is below, equal to or above o.
func (v Version) Compare(o Version) int {
	for i := range v.parts {
		if c := v.parts[i].Cmp(o.parts[i]); c != 0 {
			return c
		}
	}
	return 0
}

// CompareLenient orders two strings under the lenient rule.
func CompareLenient(a, b string) int { return lenient(a).Compare(lenient(b)) }

// Before reports whether v sorts before min under the Strict rule. A v that
// does not parse is before everything; min must parse, and a min that does
// not is a programmer error that reads as "nothing is before it".
func Before(v, min string) bool {
	a, ok := Strict(v)
	if !ok {
		return true
	}
	b, ok := Strict(min)
	if !ok {
		return false
	}
	return a.Compare(b) < 0
}

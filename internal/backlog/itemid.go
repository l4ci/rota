package backlog

import (
	"path/filepath"
	"regexp"
	"strconv"
	"unicode"
)

// FileIDDigits is the fewest digits a file-mode item ID carries. IDs are
// minted zero-padded (B07, F12), and a detail file, design or proof is
// addressed by that spelling, so B7 would name a second, unrelated file.
// Issue-mode IDs are bare issue numbers and may have any count (1).
const FileIDDigits = 2

// IDPattern is the regexp source of an item ID with at least minDigits
// digits, with no capture group and no anchors: embed it in a larger pattern.
// Digits are \p{Nd} for the same reason the rest of the package uses it.
func IDPattern(minDigits int) string {
	if minDigits < 1 {
		minDigits = 1
	}
	return `[` + ItemLetters + `]\p{Nd}{` + strconv.Itoa(minDigits) + `,}`
}

// BracketedIDRe matches a bracketed item ID such as [B07]; submatch 1 is the
// ID without brackets. It is IDPattern(1) in a capture group.
var BracketedIDRe = regexp.MustCompile(`\[(` + IDPattern(1) + `)\]`)

// AnyTypeIDPattern is IDPattern(1) widened to every registered type letter,
// the slice S included. Done lines can carry a slice's key, so reading them
// back needs it; nothing that mints or validates an item ID does.
const AnyTypeIDPattern = `[` + ItemLetters + `S]\p{Nd}+`

// MilestonePattern and SlicePattern are the regexp sources of a milestone ID
// (M01) and of the slice part of a plan key (S1), unanchored like IDPattern.
const (
	MilestonePattern = `M\p{Nd}{2,}`
	SlicePattern     = `S\p{Nd}+`
)

// ValidID reports whether id is exactly one item ID: a type letter from
// ItemLetters followed by at least minDigits digits.
func ValidID(id string, minDigits int) bool {
	if minDigits < 1 {
		minDigits = 1
	}
	n := 0
	for i, r := range id {
		switch {
		case i == 0:
			if r > unicode.MaxASCII || !containsLetter(r) {
				return false
			}
		case unicode.Is(unicode.Nd, r):
			n++
		default:
			return false
		}
	}
	return n >= minDigits
}

func containsLetter(r rune) bool {
	for _, l := range ItemLetters {
		if l == r {
			return true
		}
	}
	return false
}

// ItemType is the type letter of an ID that starts with one, "" otherwise.
// It does not check the digits: pair it with ValidID where that matters.
func ItemType(id string) string {
	if id != "" && containsLetter(rune(id[0])) {
		return id[:1]
	}
	return ""
}

// DetailPath is the file-mode detail file of an item,
// <root>/.rota/<bugs|features|tasks>/<ID>.md, "" when the ID's prefix has no
// detail directory.
func DetailPath(root, id string) string {
	dir := detailDir(id)
	if dir == "" {
		return ""
	}
	return filepath.Join(root, ".rota", dir, id+".md")
}

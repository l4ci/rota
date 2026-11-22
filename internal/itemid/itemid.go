// Package itemid is the item-ID grammar: pure string rules for the IDs a
// backlog item goes by (B07, #42, F42, PROJ-123). It is a leaf: backlog,
// design, plan, landfreeze, migrate and round read the grammar from here
// instead of pulling in the backlog package.
package itemid

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// ItemLetters are the letters of the types that appear as bullets in the open
// backlog sections (ITEM_TYPES).
const ItemLetters = "BFT"

// IssueRef is one unqualified item reference on the issue backend: a number
// ("42", "#42"), a lettered number ("F42") or a tracker key ("PROJ-123").
// It is the single owner of that grammar: backlog lookups, plan and design
// note access and the acceptance verbs all read references through
// ParseIssueRef.
type IssueRef struct {
	// ID is the normalized spelling: no "#", upper case, digits as typed
	// ("#42" gives "42", "f07" gives "F07", "proj-1" gives "PROJ-1").
	ID string
	// Key is the tracker key the reference resolves to.
	Key tracker.IssueKey
	// Letter is the type letter of a lettered number, "" otherwise.
	Letter string
}

// ParseIssueRef reads s as an item reference. Unlike the lookups, which
// ignore surrounding space, it takes the text exactly: a reference that
// holds whitespace is not one.
func ParseIssueRef(s string) (IssueRef, error) {
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return IssueRef{}, fmt.Errorf("not an item reference: %q", s)
	}
	key, letter, err := ResolveKey(s)
	if err != nil {
		return IssueRef{}, err
	}
	return IssueRef{ID: tracker.NormRef(s), Key: key, Letter: letter}, nil
}

var itemRefRe = regexp.MustCompile(`(?i)\A(?:#(\p{Nd}+)|([` + ItemLetters + `])?(\p{Nd}+))\z`)

// ResolveItemRef is the retired Python resolve_item_ref: "#42", "42" or "F42" give
// (42, "") or (42, "F"); the letter is upper-cased.
func ResolveItemRef(ref string) (n int, letter string, err error) {
	s := pystr.Strip(ref)
	m := itemRefRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", fmt.Errorf("not an item reference: %q", s)
	}
	digits := m[1]
	if digits == "" {
		digits = m[3]
	}
	if n, err = Atoi(digits); err != nil {
		return 0, "", err
	}
	if m[2] != "" {
		letter = string(upper(m[2][0]))
	}
	return n, letter, nil
}

// ResolveKey is ResolveItemRef widened to a key-based tracker: a ref the item
// grammar reads ("#42", "F42") gives the numbered key, and a tracker key
// ("PROJ-123", "proj-123") gives itself. letter is "" for a key.
func ResolveKey(ref string) (key tracker.IssueKey, letter string, err error) {
	if n, letter, err := ResolveItemRef(ref); err == nil {
		return tracker.NumKey(n), letter, nil
	}
	if k, ok := tracker.ParseKey(pystr.Strip(ref)); ok {
		return k, "", nil
	}
	return "", "", fmt.Errorf("not an item reference: %q", pystr.Strip(ref))
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

// Atoi reads a run of Unicode decimal digits (Python's int() accepts them).
func Atoi(digits string) (int, error) {
	n := 0
	for _, r := range digits {
		d := pystr.DigitValue(r)
		if n > (int(^uint(0)>>1)-d)/10 {
			return 0, fmt.Errorf("number out of range: %s", digits)
		}
		n = n*10 + d
	}
	return n, nil
}

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

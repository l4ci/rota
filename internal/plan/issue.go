package plan

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
)

// Issue mode, item plans: the plan is a `plan` note on the item's issue and
// the milestone part of the key is ignored. Slice plans (<M>-S<NN>) are
// notes on the milestone's tracking issue and live with the milestone verbs.

var (
	issueAddUnitRe = regexp.MustCompile(`^[BFTS]\d+$`)
	itemKeyRe      = regexp.MustCompile(`^M\d{2,}-([BFT]\d+)$`)
	digitsRe       = regexp.MustCompile(`^\d+$`)
)

// ItemOf splits a plan key into its item ref when it is an item plan
// (M01-B07 gives "B07"); isItem is false for a slice key (M01-S03).
func ItemOf(key string) (item string, isItem bool, err error) {
	if ItemOnlyKey(key) {
		return strings.ToUpper(strings.TrimPrefix(key, "#")), true, nil
	}
	if err := checkKey(key); err != nil {
		return "", false, err
	}
	if m := itemKeyRe.FindStringSubmatch(key); m != nil {
		return m[1], true, nil
	}
	return "", false, nil
}

func noteMissing(item string) *artifact.Error {
	return artifact.Errf(artifact.ExitResolution, "plan note for %s not found", item)
}

// AddItemNote creates the plan note on the item's issue for the explicit key
// o.Key (an item plan); an existing note is exit 4, an unknown item exit 3.
func AddItemNote(root string, n artifact.Notes, o AddOpts) (key string, err error) {
	milestone, unit, err := parseAdd(o, true)
	if err != nil {
		return "", err
	}
	design, repo, err := extras(root, o, true)
	if err != nil {
		return "", err
	}
	if unit == "" || !(issueAddUnitRe.MatchString(unit) || milestone == "" && digitsRe.MatchString(unit)) || strings.HasPrefix(unit, "S") {
		return "", artifact.Errf(artifact.ExitUsage, "unit must be an item ID like B7/F3/T11 for an item plan, got %q", unit)
	}
	if _, ok, err := n.NoteGet(unit, "plan"); err != nil {
		return "", err
	} else if ok {
		return "", artifact.Errf(artifact.ExitRefused, "plan note for %s already exists", unit)
	}
	key = milestone + "-" + unit
	if milestone == "" {
		key = o.Key // milestone-free: the key is the item ref as given
	}
	_, err = n.NotePut(unit, "plan", stub(key, milestone, unit, "item", repo, design, o.Title))
	return key, err
}

// ShowItemNote is the item's plan note; the old helper printed it with a newline.
func ShowItemNote(n artifact.Notes, item string) (string, error) {
	text, ok, err := n.NoteGet(item, "plan")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", noteMissing(item)
	}
	return text + "\n", nil
}

// PutItemNote replaces an existing plan note; changed is false when it
// already reads as text.
func PutItemNote(n artifact.Notes, item, key, text string) (bool, error) {
	if _, ok, err := n.NoteGet(item, "plan"); err != nil {
		return false, err
	} else if !ok {
		return false, noteMissing(item).WithHint("rota plan add " + key + " --title <text>")
	}
	return n.NotePut(item, "plan", text)
}

// RmItemNote deletes the plan note; a missing one is exit 3.
func RmItemNote(n artifact.Notes, item string) error {
	removed, err := n.NoteRm(item, "plan")
	if err != nil {
		return err
	}
	if !removed {
		return noteMissing(item)
	}
	return nil
}

// UncertainIssue is Uncertain for an issue-mode item already resolved to it:
// an open Major item whose issue body (without the fields block) is the
// detail. Only open items count, as only open bullets did.
func UncertainIssue(be backlog.Backend, it *backlog.Item) (typ string, reasons []string, err error) {
	if it.Closed {
		return "", nil, artifact.Errf(artifact.ExitResolution, "item %s is not open", it.ID)
	}
	typ, reasons = it.Type, []string{}
	if !strings.EqualFold(it.Tag, "major") {
		return typ, reasons, nil
	}
	detail, has, derr := be.Detail(it.ID)
	if derr != nil {
		return "", nil, derr
	}
	return typ, uncertainReasons(it.Line, detail, has), nil
}

// CheckAdd validates AddOpts the way Add (file mode) or AddItemNote (issue
// mode) does before it touches anything:
// the key or --milestone/--slice shape and --title. It writes nothing.
func CheckAdd(o AddOpts, issue bool) error {
	if _, _, err := parseAdd(o, issue); err != nil {
		return err
	}
	if o.Title == "" {
		return artifact.Errf(artifact.ExitUsage, "--title is required")
	}
	return nil
}

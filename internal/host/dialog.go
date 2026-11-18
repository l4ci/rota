package host

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file owns how rota reads a harness startup dialog: the option grammar,
// the recognition of a dialog on the prompt line (ReadDialog, isDialog) and
// the keys that accept one at spawn (DialogKeys). A harness prompt wording
// change is an edit here and a fixture in testdata, nowhere else.

// optionRow is one option of a dialog: an optional cursor (❯, or > on a plain
// terminal), an optional number (Claude Code numbered its options before
// v2.1.288), then the text.
var optionRow = regexp.MustCompile(`^\s*(❯|>)?\s*(\d+[.)]\s+)?(.*\S)`)

// option splits a line into its cursor, number and text; ok is false for a
// blank line.
func option(line string) (cursor, numbered bool, text string, ok bool) {
	m := optionRow.FindStringSubmatch(line)
	if m == nil {
		return false, false, "", false
	}
	return m[1] != "", m[2] != "", m[3], true
}

// themeOption is a row of Claude Code's first-run theme picker, whose
// highlighted choice sits after the prompt glyph like input would.
var themeOption = regexp.MustCompile(`^(?:Dark|Light) mode\b|^Auto \(match terminal\)`)

// darkRow and lightRow are the theme picker's two rows, word-bounded so
// "Dark modern" is not one.
var (
	darkRow  = regexp.MustCompile(`^Dark mode\b`)
	lightRow = regexp.MustCompile(`^Light mode\b`)
)

// numberedLine is an option line numbered "1." with no cursor: the only
// numbering the accepter trusts without a cursor. The reader also takes "1)".
var numberedLine = regexp.MustCompile(`^\s*\d+\.\s+\S`)

// readNumbered is a prompt line that starts like a numbered dialog row.
var readNumbered = regexp.MustCompile(`^\d+[.)]\s`)

// themeTitle is the theme picker's heading.
const themeTitle = "Choose the text style"

// dialogFooter is a dialog's key hint below its options. It marks an
// unnumbered choice (Claude Code's folder-trust dialog) that a number cannot.
var dialogFooter = regexp.MustCompile(`(?i)enter (to )?(confirm|select)|esc (to cancel|not now)`)

// dialogAccept are the options that accept a startup dialog.
var dialogAccept = []string{"Yes, I trust this folder", "Yes, I accept", "Yes, proceed"}

// isOnboardingTheme is Claude Code's first-run theme picker, which a config
// dir with no profile opens on: its title, or a Dark mode row followed by a
// Light mode row. Enter keeps the highlighted default, which is all a worker
// needs from it.
func isOnboardingTheme(pane string) bool {
	if strings.Contains(pane, themeTitle) {
		return true
	}
	dark := false
	for _, line := range strings.Split(pane, "\n") {
		cursor, _, text, ok := option(line)
		switch {
		case !ok:
		case darkRow.MatchString(text):
			dark = true
		case dark && !cursor && lightRow.MatchString(text):
			return true
		}
	}
	return false
}

func isDialog(line, rest string) bool {
	return themeOption.MatchString(line) || readNumbered.MatchString(line) || dialogFooter.MatchString(rest)
}

// DialogKeys reads a startup dialog off the pane text. Claude Code can open
// on the folder-trust prompt for a fresh worktree, or the Bypass Permissions
// warning on a config dir that has not accepted it yet. The two put their
// accepting option in different positions, so a fixed keypress picks "No,
// exit" on one of them. The options are the block of adjacent non-blank lines
// that holds the cursor (❯), numbered or not; with no cursor on screen, the
// block of numbered lines, cursor on the first. It returns the keys that move
// from the cursor to the accepting option plus `enter`; ok is false on an
// unknown dialog.
func DialogKeys(pane string) (keys []string, ok bool) {
	if isOnboardingTheme(pane) {
		return []string{"enter"}, true
	}
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(pane, "\n") {
		if strings.TrimSpace(line) == "" {
			if cur != nil {
				blocks, cur = append(blocks, cur), nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if cur != nil {
		blocks = append(blocks, cur)
	}
	var opts []string
	cursor := -1
	for _, b := range blocks {
		for i, line := range b {
			if c, _, _, ok := option(line); ok && c {
				opts, cursor = b, i
			}
		}
	}
	if opts == nil {
		for _, b := range blocks {
			if numberedLine.MatchString(b[0]) {
				opts, cursor = b, 0
				break
			}
		}
	}
	target := -1
	for i, line := range opts {
		_, _, text, ok := option(line)
		if !ok {
			continue
		}
		for _, a := range dialogAccept {
			if strings.Contains(text, a) {
				target = i
				break
			}
		}
		if target >= 0 {
			break
		}
	}
	if target < 0 {
		return nil, false
	}
	step, n := "down", target-cursor
	if target < cursor {
		step, n = "up", cursor-target
	}
	for i := 0; i < n; i++ {
		keys = append(keys, step)
	}
	return append(keys, "enter"), true
}

// DialogError is ErrDialogOpen with the dialog's first line, for the refusal message.
type DialogError struct{ First string }

func (e *DialogError) Error() string { return ErrDialogOpen.Error() + ": " + e.First }

func (e *DialogError) Is(target error) bool { return target == ErrDialogOpen }

// Dialog is a harness dialog standing on the prompt line.
type Dialog struct {
	First   string   // the dialog's title line
	Dismiss []string // keys that dismiss it without choosing anything; nil when unknown
}

var paneRule = regexp.MustCompile(`^[─━]{10,}$`)

// ReadDialog reports whether the pane's prompt line is a harness dialog's
// option row rather than input, with its first line and, for the dialogs whose
// dismissal is known to be safe, the keys. Claude Code's autocompact picker
// says "esc not now", so Escape declines it; any other dialog stays unknown.
func ReadDialog(pane string) (Dialog, bool) {
	i := strings.LastIndexAny(pane, promptMarkers)
	if i < 0 {
		return Dialog{}, false
	}
	_, size := utf8.DecodeRuneInString(pane[i:])
	rest := pane[i+size:]
	line, _, _ := strings.Cut(rest, "\n")
	line = strings.TrimFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '│' })
	if !isDialog(line, rest) {
		return Dialog{}, false
	}
	// The title is the first text line below the last rule above the marker.
	var first string
	for _, l := range strings.Split(pane[:i], "\n") {
		t := strings.TrimFunc(l, func(r rune) bool { return unicode.IsSpace(r) || r == '│' })
		switch {
		case paneRule.MatchString(t):
			first = ""
		case t != "" && first == "":
			first = t
		}
	}
	if first == "" {
		first = line
	}
	d := Dialog{First: first}
	if strings.Contains(pane[:i], "Want to autocompact at") && strings.Contains(strings.ToLower(rest), "esc not now") {
		d.Dismiss = []string{"esc"}
	}
	return d, true
}

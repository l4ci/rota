package host

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/harness"
)

func composerRow(line, marker string) bool {
	if marker == "" || !strings.HasPrefix(line, marker) {
		return false
	}
	rest := line[len(marker):]
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || unicode.IsSpace(r)
}

// coreEvidenceRegions excludes the active composer and locates pending input.
// Every index is a byte offset in the original capture, including queue cuts.
func coreEvidenceRegions(screen string) (output string, queue int) {
	syntaxes := harness.InputSyntaxes()
	composer, offset := -1, 0
	for _, line := range strings.SplitAfter(screen, "\n") {
		for _, syntax := range syntaxes {
			if composerRow(line, syntax.Composer) {
				composer = offset
			}
		}
		offset += len(line)
	}
	output = screen
	if composer >= 0 {
		output = screen[:composer]
	}
	queue = -1
	for _, syntax := range syntaxes {
		for _, heading := range syntax.QueueHeadings {
			if i := strings.Index(output, heading); i >= 0 && (queue < 0 || i < queue) {
				queue = i
			}
		}
		if composer < 0 || syntax.QueueFooter == "" {
			continue
		}
		lines := strings.SplitAfter(output, "\n")
		end := len(output)
		for n := len(lines) - 1; n >= 0; n-- {
			start := end - len(lines[n])
			if strings.TrimSuffix(lines[n], "\n") == syntax.QueueFooter && syntax.QueueGap.MatchString(output[end:]) {
				// All adjacent prompt entries share this footer. Stop at agent output;
				// indented continuation rows and blank separators belong to the entries.
				at, pos := -1, start
			entries:
				for j := n - 1; j >= 0; j-- {
					pos -= len(lines[j])
					line := strings.TrimSuffix(lines[j], "\n")
					switch {
					case composerRow(line, syntax.Composer):
						at = pos
					case strings.TrimSpace(line) == "", strings.HasPrefix(line, "  "):
					default:
						break entries
					}
				}
				if at >= 0 && (queue < 0 || at < queue) {
					queue = at
				}
				break
			}
			end = start
		}
	}
	return output, queue
}

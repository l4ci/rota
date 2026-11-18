package tracker

import (
	"strings"

	"github.com/l4ci/rota/internal/marker"
)

// The key-based trackers (Jira, Linear, It's a Plan) keep the hidden fields
// block that ends an issue body outside the description. These helpers adapt
// that tracker-specific representation. marker owns the block framing, while
// backlog parses and renders the fields used by rota items.

type fieldsProp struct {
	Block string `json:"block"`
}

// splitBody is the description and the fields block of a body. A body without
// a block comes back whole with an empty block.
func splitBody(body string) (string, fieldsProp) {
	text, lines, ok := marker.SplitFields(strings.ReplaceAll(body, "\r\n", "\n"))
	if !ok {
		return body, fieldsProp{}
	}
	return text, fieldsProp{Block: marker.FieldsBlock([]string{lines})}
}

// joinBody puts a stored fields block back after the description, one blank
// line between them. An empty block leaves the text as it is.
func joinBody(text, block string) string {
	if strings.TrimSpace(block) == "" {
		return text
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return block
	}
	return text + "\n\n" + block
}

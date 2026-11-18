package tracker

import (
	"strings"

	"github.com/l4ci/rota/internal/marker"
)

// The key-based trackers (Jira, Linear, It's a Plan) keep the hidden fields
// block outside the description. This type adapts the marker-owned block to
// the tracker property shape; marker owns splitting and joining the body.

type fieldsProp struct {
	Block string `json:"block"`
}

// fieldsForBody keeps the marker-owned block in the key-based tracker property
// shape. A body without a block comes back whole with an empty property.
func fieldsForBody(body string) (string, fieldsProp) {
	text, lines, ok := marker.SplitFields(body)
	if !ok {
		return body, fieldsProp{}
	}
	return text, fieldsProp{Block: marker.FieldsBlock([]string{lines})}
}

package cli

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/extknowledge"
	"github.com/l4ci/rota/internal/knowledge"
)

// Both knowledge stores refuse a title through bulletfmt.ResolveTitle, so one
// sentinel pair serves the CLI's mapping for either store.
func TestKnowledgeStoresShareTitleSentinels(t *testing.T) {
	if !errors.Is(knowledge.ErrMultiMatch, extknowledge.ErrMultiMatch) || !errors.Is(extknowledge.ErrNotFound, knowledge.ErrNotFound) {
		t.Fatal("the stores declare their own title sentinels")
	}
}

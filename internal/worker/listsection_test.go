package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// The queue and relay log keep unknown fields on a rewrite: Edit over the raw
// sections must not drop what QueuedPR does not model.
func TestRawListSectionsKeepUnknownFieldsAndOrder(t *testing.T) {
	root := t.TempDir()
	if err := Update(root, func(d *Doc) {
		d.QueuePR(QueuedPR{Issue: "1", From: "ben", PR: "#10"})
		queueSection.Edit(d, func(l []*jsonx.Object) []*jsonx.Object { l[0].Set("extra", "keep"); return l })
		d.QueuePR(QueuedPR{Issue: "2", From: "dana", PR: "#11"})
		d.QueuePR(QueuedPR{Issue: "1", From: "ben", PR: "#10"})
		d.LogRelays("#12", []any{"r"})
	}); err != nil {
		t.Fatal(err)
	}
	reg := LoadRegistryTolerant(root)
	got := queueSection.Read(reg)
	if len(got) != 2 || jsonx.Str(got[0], "issue") != "2" || jsonx.Str(got[1], "issue") != "1" {
		t.Fatalf("queue order = %v, want 2 then re-appended 1", got)
	}
	if len(reg.LoggedRelays("#12")) != 1 {
		t.Errorf("relay log lost")
	}
}

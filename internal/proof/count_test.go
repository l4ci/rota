package proof

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
)

func TestCountRows(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       int
	}{
		{"rows only in the Proof section", "# x\n\n## Proof\n- a\n- b\nnot a row\n\n## Log\n- not proof\n", 2},
		{"no section", "# x\n", 0},
		{"empty section", "## Proof\n\n", 0},
		{"CRLF line ends", "## Proof\r\n- a\r\n- b\r\n", 2},
	} {
		if n := CountRows(c.text); n != c.want {
			t.Errorf("%s: CountRows = %d, want %d", c.name, n, c.want)
		}
	}
}

// Show and CountRows read the same lines, so a row Show returns is a row counted.
func TestCountRowsAgreesWithShow(t *testing.T) {
	text := "## Proof\r\n- a · b · c · d · e\r\n- f\r\n"
	rows, _, err := parseRows(text)
	if err != nil || len(rows) != CountRows(text) {
		t.Fatalf("parseRows = %d rows, %v; CountRows = %d", len(rows), err, CountRows(text))
	}
}

// File-mode "done requires proof" through the real proof store and counter.
func TestFileDoneRequiresProof(t *testing.T) {
	root := project(t)
	be := &backlog.File{Root: root, CountProof: CountRows}
	in := backlog.CompleteInput{Commit: "abc1234", Date: "2026-10-05", Reason: "done"}
	if _, err := be.Complete("B07", in); !errors.Is(err, backlog.ErrProofMissing) {
		t.Fatalf("without proof: %v", err)
	}
	o := AddOpts{Check: "unit tests", Result: "PASS", Evidence: "go test ok", Sha: "abc1234"}
	if _, _, err := Add(Files(root), root, "B07", o); err != nil {
		t.Fatal(err)
	}
	if changed, err := be.Complete("B07", in); err != nil || !changed {
		t.Fatalf("with proof: changed=%v err=%v", changed, err)
	}
}

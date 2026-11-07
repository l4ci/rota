package proof

import "testing"

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

package round

import (
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

const legacyReview = `{"architectureReview":{"at":"A","seeded":true,"round":2,"trigger":"t","items":["1"],"closed":{"checked":"C","since":"S","count":3}}}`

// The architectureReview wire shape is pinned against raw JSON, not through
// ReviewOf, so a dropped or renamed key in the codec fails here.
func TestReviewWireShape(t *testing.T) {
	cases := []struct {
		name string
		op   func(*worker.Doc)
		want string
	}{
		{"seed", func(d *worker.Doc) { SeedReview(d, "B") },
			`{"at":"B","seeded":true,"round":2,"trigger":"t","items":["1"],"closed":{"checked":"C","since":"S","count":3}}`},
		{"cache closed", func(d *worker.Doc) { CacheClosedCount(d, "C2", "S2", 5) },
			`{"at":"A","seeded":true,"round":2,"trigger":"t","items":["1"],"closed":{"checked":"C2","since":"S2","count":5}}`},
		{"record items", func(d *worker.Doc) { RecordReviewItems(d, []string{"2"}) },
			`{"at":"A","seeded":true,"round":2,"trigger":"t","items":["1","2"],"closed":{"checked":"C","since":"S","count":3}}`},
		{"start", func(d *worker.Doc) { StartReview(d, "N", 3, "x", []string{"9"}) },
			`{"at":"N","round":3,"trigger":"x","items":["1","9"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			path := worker.RegistryPath(root)
			if err := os.MkdirAll(path[:len(path)-len("/workers.json")], 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(legacyReview), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := worker.Update(root, c.op); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			v, err := jsonx.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			sec, _ := v.(*jsonx.Object).Get("architectureReview")
			got, err := jsonx.MarshalCompact(sec)
			if err != nil {
				t.Fatal(err)
			}
			if compact(got) != c.want {
				t.Errorf("architectureReview = %s\nwant %s", got, c.want)
			}
		})
	}
}

// A never-seeded review writes no empty `at`.
func TestReviewOmitsEmptyAt(t *testing.T) {
	root := t.TempDir()
	if err := worker.Update(root, func(d *worker.Doc) { RecordReviewItems(d, []string{"1"}) }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(worker.RegistryPath(root))
	v, _ := jsonx.Decode(raw)
	sec, _ := v.(*jsonx.Object).Get("architectureReview")
	got, _ := jsonx.MarshalCompact(sec)
	if compact(got) != `{"items":["1"]}` {
		t.Errorf("architectureReview = %s", got)
	}
}

// compact drops the spaces jsonx puts after colons and commas; no test value has any.
func compact(b []byte) string { return strings.ReplaceAll(string(b), " ", "") }

package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
)

func TestEditBody(t *testing.T) {
	body := "Intro\n\n" + "<!-- rota:fields\nRelated: #2\nSubsystem: x\n-->"
	key := tracker.NumKey(1)
	newIssues := func() (*Issues, *trackertest.Fake) {
		f := &trackertest.Fake{Issues: []tracker.Issue{{Key: key, Title: "T", State: "open", Body: body}}}
		return &Issues{Tracker: f}, f
	}
	t.Run("splits, mutates then renders in order", func(t *testing.T) {
		b, f := newIssues()
		ok, err := b.editBody(f, key, body, func(d *BodyDoc) (bool, error) {
			if d.Text != "Intro\n\n" && strings.TrimSpace(d.Text) != "Intro" {
				t.Fatalf("text = %q", d.Text)
			}
			if d.Fields["Related"] != "#2" || len(d.Order) != 2 || d.Order[0] != "Related" {
				t.Fatalf("fields = %v order = %v", d.Fields, d.Order)
			}
			d.Text = "Changed"
			d.Fields["Since"] = "now"
			d.Order = append(d.Order, "Since")
			return true, nil
		})
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		got := f.Issues[0].Body
		want := RenderFieldsBlock("Changed", []string{"Related", "Subsystem", "Since"}, map[string]string{"Related": "#2", "Subsystem": "x", "Since": "now"})
		if got != want {
			t.Fatalf("body = %q want %q", got, want)
		}
	})
	t.Run("no change writes nothing", func(t *testing.T) {
		b, f := newIssues()
		ok, err := b.editBody(f, key, body, func(d *BodyDoc) (bool, error) { return false, nil })
		if err != nil || ok || f.Issues[0].Body != body {
			t.Fatalf("ok=%v err=%v body=%q", ok, err, f.Issues[0].Body)
		}
	})
	t.Run("raw replace is verbatim and an identical body is skipped", func(t *testing.T) {
		b, f := newIssues()
		ok, err := b.editBody(f, key, body, func(d *BodyDoc) (bool, error) { d.SetRaw("plain {x}"); return true, nil })
		if err != nil || !ok || f.Issues[0].Body != "plain {x}" {
			t.Fatalf("ok=%v err=%v body=%q", ok, err, f.Issues[0].Body)
		}
		ok, err = b.editBody(f, key, "plain {x}", func(d *BodyDoc) (bool, error) { d.SetRaw("plain {x}"); return true, nil })
		if err != nil || ok {
			t.Fatalf("identical raw wrote: ok=%v err=%v", ok, err)
		}
	})
}

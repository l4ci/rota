package release

import (
	"errors"
	"testing"
)

func TestUpdateChangelogRefusesUnreleased(t *testing.T) {
	for _, heading := range []string{"## Unreleased", "## unreleased", "## UNRELEASED — soon", "## Unreleased\n"} {
		content := "# Changelog\n\n" + heading + "\n\n- stale\n\n## v1.0.0 — 2020-01-01\n\nold\n"
		out, err := UpdateChangelog(content, true, "1.1.0", "2026-01-01", "- new")
		if !errors.Is(err, ErrUnreleased) || out != "" {
			t.Errorf("%q: out %q err %v, want ErrUnreleased", heading, out, err)
		}
	}
}

func TestUpdateChangelogIgnoresUnreleasedMentions(t *testing.T) {
	for _, content := range []string{
		"# Changelog\n\n- fold the ## Unreleased block\n- Unreleased things\n",
		"# Changelog\n\n### Unreleased\n",
		"# Changelog\n\n## Unreleasedish notes\n\n",
		"# Changelog\n\n    ## Unreleased\n",
	} {
		if _, err := UpdateChangelog(content, true, "1.1.0", "2026-01-01", "- new"); err != nil {
			t.Errorf("%q: %v", content, err)
		}
	}
}

func TestUpdateChangelogNoUnreleasedUnchanged(t *testing.T) {
	out, err := UpdateChangelog("# Changelog\n\n## v1.0.0 — 2020-01-01\n\nold\n", true, "1.1.0", "2026-01-01", "- new")
	want := "# Changelog\n\n## v1.1.0 — 2026-01-01\n\n- new\n\n## v1.0.0 — 2020-01-01\n\nold\n"
	if err != nil || out != want {
		t.Errorf("out %q err %v", out, err)
	}
}

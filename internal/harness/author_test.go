package harness

import "testing"

func TestResolveAuthor(t *testing.T) {
	for _, c := range []struct {
		name string
		in   AuthorSources
		kind string
		from string
	}{
		{"flag", AuthorSources{Flag: OpenCode, Slot: Codex, Assigned: Claude, Labels: []string{"harness:claude"}, Project: Claude}, OpenCode, AuthorFromFlag},
		{"holding slot", AuthorSources{Slot: Codex, Assigned: OpenCode, Labels: []string{"harness:claude"}, Project: Claude}, Codex, AuthorFromSlot},
		{"historical assignment", AuthorSources{Assigned: Codex, Labels: []string{"harness:claude"}, Project: Claude}, Codex, AuthorFromAssignment},
		{"label overrides resume", AuthorSources{Labels: []string{"harness:opencode"}, ResumeSlot: Codex, Project: Claude}, OpenCode, AuthorFromLabel},
		{"resumed slot", AuthorSources{ResumeSlot: Codex, Project: Claude}, Codex, AuthorFromResume},
		{"project overrides prior slot", AuthorSources{Project: Codex, PriorSlot: Claude}, Codex, AuthorFromProject},
		{"prior slot without project", AuthorSources{PriorSlot: Codex}, Codex, AuthorFromSlot},
		{"default", AuthorSources{}, Claude, AuthorFromDefault},
		{"unrelated labels", AuthorSources{Labels: []string{"size:Major", "model:x"}}, Claude, AuthorFromDefault},
		{"unknown and empty labels", AuthorSources{Labels: []string{"harness:vim", "harness:"}, Project: Codex}, Codex, AuthorFromProject},
		{"first valid label", AuthorSources{Labels: []string{"harness:vim", "harness:opencode", "harness:claude"}}, OpenCode, AuthorFromLabel},
		{"flag overrides resumed slot", AuthorSources{Flag: Claude, ResumeSlot: Codex, Project: OpenCode}, Claude, AuthorFromFlag},
		{"resume without recorded kind", AuthorSources{Project: OpenCode}, OpenCode, AuthorFromProject},
	} {
		t.Run(c.name, func(t *testing.T) {
			kind, from := ResolveAuthor(c.in)
			if kind != c.kind || from != c.from {
				t.Fatalf("ResolveAuthor(%+v) = %q from %q, want %q from %q", c.in, kind, from, c.kind, c.from)
			}
		})
	}
}

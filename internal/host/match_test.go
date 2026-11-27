package host

import "testing"

func TestMatchAgent(t *testing.T) {
	kit, dana := AgentName("kit", "w1:t3"), AgentName("dana", "w1:t4")
	for _, tc := range []struct {
		name    string
		kind    Kind
		handle  string
		agents  []Agent
		want    int
		movedTo string
	}{
		{"in its tab", Herdr, "w1:t3", []Agent{{Name: kit, Tab: "w1:t3", Pane: "w1:p1", TabPanes: 1}}, 0, ""},
		{"moved to another tab", Herdr, "w1:t3", []Agent{{Name: dana, Tab: "w2:t1"}, {Name: kit, Tab: "w2:t1", Pane: "w2:p2", TabPanes: 1}}, 1, "w2:t1"},
		{"recorded tab now shared", Herdr, "w1:t3", []Agent{{Name: kit, Tab: "w1:t3", Pane: "w1:p9", TabPanes: 2}}, 0, "w1:p9"},
		{"name beats a shared tab", Herdr, "w1:t3", []Agent{{Name: dana, Tab: "w1:t3"}, {Name: kit, Tab: "w1:t3"}}, 1, ""},
		{"tab only is not a move", Herdr, "w1:t3", []Agent{{Name: "other", Tab: "w1:t3", Pane: "w1:p9", TabPanes: 2}}, 0, ""},
		{"nobody", Herdr, "w1:t5", []Agent{{Name: kit, Tab: "w2:t1"}}, -1, ""},
		{"no handle", Herdr, "", []Agent{{Name: AgentName("kit", "")}}, -1, ""},
		{"tmux has no agent names", Tmux, "w1:t3", []Agent{{Name: kit, Tab: "w2:t1"}}, -1, ""},
		{"core matches by tab", HerdrCore, "h1", []Agent{{Name: "kit", Tab: "h1"}}, 0, ""},
		{"orca matches by tab", Orca, "h1", []Agent{{Name: "title", Tab: "h1"}}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, movedTo := CapabilitiesForKind(tc.kind).MatchAgent(tc.agents, "kit", tc.handle)
			if i != tc.want || movedTo != tc.movedTo {
				t.Errorf("MatchAgent = %d, %q; want %d, %q", i, movedTo, tc.want, tc.movedTo)
			}
		})
	}
}

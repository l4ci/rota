package escalation

import (
	"strings"
	"testing"
)

func TestComposeListsOptionsRecommendationAndCost(t *testing.T) {
	got := Compose("e1", "Keep the flag?", "Body text.", Choice{
		Options: []string{"keep it", "drop it", "deprecate"}, Recommend: 3, Cost: "a second breaking release",
	})
	for _, want := range []string{
		"1) keep it\n",
		"2) drop it\n",
		"3) deprecate (recommended)\n",
		"Recommended: 3) deprecate\n",
		"Cost if wrong: a second breaking release\n",
		"Reply `ok` or `yes` to take the recommendation, or the option number or letter (A is 1).",
		"<!-- rota:escalation e1 -->",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comment lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "1) keep it (recommended)") {
		t.Errorf("only the recommended option is marked:\n%s", got)
	}
}

func TestComposeWithoutChoiceKeepsTheOldShape(t *testing.T) {
	got := Compose("e1", "t", "b", Choice{})
	if strings.Contains(got, "Cost if wrong") || strings.Contains(got, "1)") {
		t.Errorf("a choiceless escalation lists nothing:\n%s", got)
	}
}

func TestResolveOption(t *testing.T) {
	opts := []string{"a", "b", "c"}
	for _, c := range []struct {
		reply string
		want  int
	}{
		{"ok", 2}, {"OK.", 2}, {"yes", 2}, {"Yes!", 2}, {"\n  ok  \n", 2},
		{"1", 1}, {"3", 3}, {"option 3", 3}, {"2.", 2},
		{"a", 1}, {"C", 3}, {"option b", 2}, {"d", 0}, {"0", 0}, {"4", 0}, {"ok but only for the docs", 0}, {"no", 0}, {"", 0}, {"take option two", 0},
	} {
		if got := ResolveOption(opts, 2, c.reply); got != c.want {
			t.Errorf("ResolveOption(%q) = %d, want %d", c.reply, got, c.want)
		}
	}
	if got := ResolveOption(nil, 0, "ok"); got != 0 {
		t.Errorf("an escalation without options resolves nothing, got %d", got)
	}
}

func TestRecommendedFirst(t *testing.T) {
	got := Choice{Options: []string{"a", "b", "c"}, Recommend: 3, Cost: "x"}.RecommendedFirst()
	if strings.Join(got.Options, ",") != "c,a,b" || got.Recommend != 1 || got.Cost != "x" {
		t.Errorf("got %+v", got)
	}
	if same := (Choice{Options: []string{"a"}, Recommend: 0}).RecommendedFirst(); same.Recommend != 0 {
		t.Errorf("an invalid recommendation is left alone: %+v", same)
	}
}

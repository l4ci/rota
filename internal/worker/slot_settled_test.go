package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestSlotSettledAnswersMigration(t *testing.T) {
	o := jsonx.NewObject()
	o.Set("briefDecisions", "m: Keep the legacy answer.")
	s := AsSlot(o)
	if got := s.BriefSettledAnswers(); got != "m: Keep the legacy answer." {
		t.Fatalf("legacy answer: %q", got)
	}
	s.SetBriefContext("m: Use the revised answer.", []string{"13"})
	if got := jsonx.Str(s.Raw(), "briefSettledAnswers"); got != "m: Use the revised answer." {
		t.Fatalf("saved answer: %q", got)
	}
	if _, ok := s.Raw().Get("briefDecisions"); ok {
		t.Fatal("saving context retained the legacy key")
	}
	s.SetBriefContext("", nil)
	if s.BriefSettledAnswers() != "" || len(s.BriefSiblings()) != 0 {
		t.Fatal("cleared context retained answers or siblings")
	}
	if _, ok := s.Raw().Get("briefSettledAnswers"); ok {
		t.Fatal("clearing context retained the new key")
	}
}

func TestSlotSettledAnswersPreferNewKey(t *testing.T) {
	for _, answer := range []string{"m: Use the new answer.", ""} {
		o := jsonx.NewObject()
		o.Set("briefDecisions", "m: Ignore the old answer.")
		o.Set("briefSettledAnswers", answer)
		s := AsSlot(o)
		if got := s.BriefSettledAnswers(); got != answer {
			t.Fatalf("new answer %q: got %q", answer, got)
		}
		s.SetBriefContext("", nil)
		if s.BriefSettledAnswers() != "" {
			t.Fatal("clearing context restored a legacy answer")
		}
	}
}

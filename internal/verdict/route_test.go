package verdict

import "testing"

func TestProducerNext(t *testing.T) {
	cases := []struct{ kind, v, want string }{
		{ReviewSpec, Pass, NextQuality},
		{ReviewSpec, Concerns, NextQuality},
		{ReviewSpec, Fail, NextReport},
		{ReviewQuality, Pass, NextReport},
		{ReviewQuality, Fail, NextReport},
		{SecondOpinion, Concerns, NextReport},
		{QA, InfraFail, NextReport},
	}
	for _, c := range cases {
		if got := ProducerNext(c.kind, c.v); got != c.want {
			t.Errorf("ProducerNext(%s, %s) = %s, want %s", c.kind, c.v, got, c.want)
		}
	}
}

func TestDebugNext(t *testing.T) {
	cases := []struct {
		v      string
		failed int
		want   string
	}{
		{Pass, 0, NextComplete},
		{Pass, 5, NextComplete},
		{Fail, 1, NextHypothesize},
		{Fail, 2, NextHypothesize},
		{Fail, 3, NextHalt},
		{Fail, 4, NextHalt},
	}
	for _, c := range cases {
		if got := DebugNext(c.v, c.failed); got != c.want {
			t.Errorf("DebugNext(%s, %d) = %s, want %s", c.v, c.failed, got, c.want)
		}
	}
}

// TestRouteTable is the contract's routing table, row by row.
func TestRouteTable(t *testing.T) {
	off := Settings{Autonomy: "off", QAGate: "advisory", Runner: "subagent"}
	auto := Settings{Autonomy: "auto", QAGate: "advisory", Runner: "subagent"}
	loop := Settings{Autonomy: "loop", QAGate: "advisory", Runner: "subagent"}
	codex := Settings{Autonomy: "off", Runner: "codex"}
	codexLoop := Settings{Autonomy: "loop", Runner: "codex"}
	blocking := Settings{Autonomy: "off", QAGate: "blocking"}
	blockingLoop := Settings{Autonomy: "loop", QAGate: "blocking"}
	unset := Settings{}
	cases := []struct {
		consumer string
		s        Settings
		v, want  string
	}{
		{"ship-review", off, Pass, NextContinue},
		{"ship-review", off, Concerns, NextAsk},
		{"ship-review", auto, Concerns, NextAsk},
		{"ship-review", loop, Concerns, NextAddress},
		{"ship-review", off, Fail, NextStop},
		{"ship-review", loop, Fail, NextStop},

		{"ship-second-opinion", off, Pass, NextContinue},
		{"ship-second-opinion", off, Concerns, NextAsk},
		{"ship-second-opinion", loop, Concerns, NextAddress},
		{"ship-second-opinion", loop, Fail, NextStop},
		{"ship-second-opinion", codex, Pass, NextContinue},
		{"ship-second-opinion", codex, Concerns, NextSurface},
		{"ship-second-opinion", codexLoop, Fail, NextSurface},

		{"ship-qa", off, Pass, NextContinue},
		{"ship-qa", off, Concerns, NextSurface},
		{"ship-qa", loop, Fail, NextSurface},
		{"ship-qa", off, InfraFail, NextSurface},
		{"ship-qa", unset, Fail, NextSurface}, // qa.gate defaults to advisory
		{"ship-qa", blocking, Pass, NextContinue},
		{"ship-qa", blocking, Concerns, NextAsk},
		{"ship-qa", blockingLoop, Concerns, NextAddress},
		{"ship-qa", blocking, Fail, NextStop},
		{"ship-qa", blocking, InfraFail, NextSurface},
		{"ship-qa", blockingLoop, InfraFail, NextSurface},

		{"queue", off, Pass, NextAsk},
		{"queue", loop, Pass, NextMerge},
		{"queue", off, Concerns, NextRequestChanges},
		{"queue", loop, Fail, NextRequestChanges},
	}
	for _, c := range cases {
		if got := Route(c.consumer, c.v, c.s); got != c.want {
			t.Errorf("Route(%s, %s, %+v) = %s, want %s", c.consumer, c.v, c.s, got, c.want)
		}
	}
}

func TestAdvisory(t *testing.T) {
	if Advisory("ship-review", Settings{Runner: "codex", QAGate: "advisory"}) {
		t.Error("ship-review is never advisory")
	}
	if !Advisory("ship-second-opinion", Settings{Runner: "codex"}) || Advisory("ship-second-opinion", Settings{Runner: "subagent"}) {
		t.Error("second opinion is advisory only under the codex runner")
	}
	if !Advisory("ship-qa", Settings{QAGate: "advisory"}) || Advisory("ship-qa", Settings{QAGate: "blocking"}) {
		t.Error("qa is advisory unless qa.gate is blocking")
	}
}

func TestForConsumer(t *testing.T) {
	list := []Record{rec(ReviewSpec, Fail, "a"), rec(SecondOpinion, Concerns, "a"), rec(QA, InfraFail, "a")}
	for consumer, want := range map[string]string{
		"ship-review":         ReviewSpec,
		"queue":               ReviewSpec,
		"ship-second-opinion": SecondOpinion,
		"ship-qa":             QA,
	} {
		if r, ok := ForConsumer(consumer, list); !ok || r.Kind != want {
			t.Errorf("%s read %q", consumer, r.Kind)
		}
	}
	if _, ok := ForConsumer("ship-qa", list[:2]); ok {
		t.Error("missing qa verdict found")
	}
}

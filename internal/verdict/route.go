package verdict

// Producer next steps.
const (
	NextQuality     = "quality"     // review-spec passed: run Stage 2
	NextReport      = "report"      // the producer is done: relay or return
	NextComplete    = "complete"    // the debug fix held
	NextHypothesize = "hypothesize" // the debug fix failed: back to Step 6
	NextHalt        = "halt"        // the Iron Law
)

// Consumer next steps.
const (
	NextContinue       = "continue"
	NextAsk            = "ask"     // off/auto: ask the user
	NextSurface        = "surface" // show the findings and continue
	NextStop           = "stop"
	NextRequestChanges = "request-changes"
)

// Consumers are the values `verdict route --for` accepts.
var Consumers = []string{"ship-review", "ship-second-opinion", "ship-qa", "queue"}

// ForConsumer picks the record a consumer routes on from a branch's list:
// the effective review verdict for ship-review and queue, else the latest
// record of the consumer's kind.
func ForConsumer(consumer string, list []Record) (Record, bool) {
	switch consumer {
	case "ship-second-opinion":
		return Latest(list, SecondOpinion)
	case "ship-qa":
		return Latest(list, QA)
	}
	return EffectiveReview(list)
}

// ProducerNext is the step after recording a verdict of kind.
func ProducerNext(kind, v string) string {
	if kind == ReviewSpec && v != Fail {
		return NextQuality
	}
	return NextReport
}

// DebugNext is the step after a debug fix verdict, given the item's
// failed-fix count including this one.
func DebugNext(v string, failed int) string {
	switch {
	case v == Pass:
		return NextComplete
	case failed >= IronLaw:
		return NextHalt
	}
	return NextHypothesize
}

// Settings are the config values consumer routing reads.
type Settings struct {
	QAGate string // qa.gate
	Runner string // ship.secondOpinionRunner
}

// Advisory reports whether a consumer never blocks under s: a second
// opinion from the retired codex runner (#158), or QA under an advisory
// gate.
func Advisory(consumer string, s Settings) bool {
	switch consumer {
	case "ship-second-opinion":
		return s.Runner == "codex"
	case "ship-qa":
		return s.QAGate != "blocking"
	}
	return false
}

// Route is the consumer's next step for verdict v. consumer must be one
// of Consumers.
func Route(consumer, v string, s Settings) string {
	if consumer == "queue" {
		if v != Pass {
			return NextRequestChanges
		}
		return NextAsk
	}
	switch {
	case v == Pass:
		return NextContinue
	case v == InfraFail, Advisory(consumer, s):
		// QA that could not run never blocks a ship: the product was not
		// judged, so there is nothing to stop on.
		return NextSurface
	case v != Concerns:
		return NextStop
	}
	return NextAsk
}

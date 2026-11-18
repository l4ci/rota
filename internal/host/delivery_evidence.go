package host

import (
	"encoding/hex"
	"strings"

	"github.com/l4ci/rota/internal/harness"
)

// deliveryEcho describes text evidence without classifying pickup or queueing.
// Each adapter chooses its output region and, when available, checks freshness
// against a pre-send capture. A sequence alone is weaker than the full opening
// or a valid signature; an arbitrary brief tail is sufficient only for herdr.
type deliveryEcho struct {
	Sequence, Opening, Signature, Tail bool
}

// relayDeliveryEvidence matches this payload's echo in an output region. It
// never treats a marker quoted in prose as an opening or sequence echo.
func relayDeliveryEvidence(output, text string) deliveryEcho {
	marker, opening, signature := "", "", ""
	for _, line := range strings.Split(text, "\n") {
		if seq := harness.RelaySequence(line); marker == "" && seq != "" {
			marker = seq
		}
		if opening == "" && harness.IsRelayOpen(line) && harness.RelaySequence(line) != "" {
			opening = strings.TrimSpace(line)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- ROTA-SIG ") && strings.HasSuffix(line, " ---") {
			digest := strings.TrimSuffix(strings.TrimPrefix(line, "--- ROTA-SIG "), " ---")
			if b, err := hex.DecodeString(digest); err == nil && len(b) == 32 {
				signature = line
			}
		}
	}
	echo := deliveryEcho{}
	for _, line := range strings.Split(output, "\n") {
		if marker != "" && harness.RelaySequence(line) == marker {
			echo.Sequence = true
		}
		if opening != "" && harness.IsRelayOpen(line) && strings.HasSuffix(strings.TrimSpace(line), opening) {
			echo.Opening = true
		}
	}
	flat := squeeze(output)
	echo.Signature = signature != "" && strings.Contains(flat, squeeze(signature))
	tail := briefTail(text)
	echo.Tail = tail != "" && strings.Contains(flat, tail)
	return echo
}

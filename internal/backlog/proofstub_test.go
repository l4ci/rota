package backlog

import "strings"

// stubCountProof stands in for proof.CountRows, which backlog cannot import
// (proof imports backlog): it counts every "- " line. The real counter is
// exercised through the seam in proofseam_test.go.
func stubCountProof(text string) int { return strings.Count("\n"+text, "\n- ") }

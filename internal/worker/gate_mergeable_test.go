package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateForgeMergeable: the forge's mergeability answer is read before the
// scratch verify, so a PR the forge cannot merge costs no verify run.
func TestGateForgeMergeable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		forge      map[string]string
		opts       GateOpts
		pr         string
		verdict    string
		verified   bool // test.full ran
		asks       int  // PRMergeable calls
		errHas     string
		wantLanded bool
	}{
		{name: "conflicting ends before the verify", forge: map[string]string{"mergeable": "conflict", "mergeReason": "DIRTY"},
			verdict: GateMergeFailed, asks: 1, errHas: "DIRTY"},
		{name: "mergeable verifies and merges", forge: map[string]string{"mergeable": "clean"},
			verdict: GatePass, verified: true, asks: 1, wantLanded: true},
		{name: "unknown retries, then proceeds", forge: map[string]string{"mergeable": "clean", "unknownFor": "9"},
			verdict: GatePass, verified: true, asks: 3, wantLanded: true},
		{name: "unknown then conflicting is refused", forge: map[string]string{"mergeable": "conflict", "mergeReason": "DIRTY", "unknownFor": "1"},
			verdict: GateMergeFailed, asks: 2, errHas: "DIRTY"},
		{name: "a pre-check error proceeds", forge: map[string]string{"mergeable": "error"},
			verdict: GatePass, verified: true, asks: 1, wantLanded: true},
		{name: "no-verify skips the question", forge: map[string]string{"mergeable": "conflict", "mergeReason": "DIRTY"},
			opts: GateOpts{NoVerify: true}, verdict: GateMergeFailed, asks: 0, errHas: "merge failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			marker := filepath.Join(t.TempDir(), "verified")
			w.setConfig(`{"test":{"full":["touch ` + marker + `"]}}`)
			for k, v := range tc.forge {
				w.forge(k, v)
			}
			if tc.opts.NoVerify {
				w.mode = "fail" // the post-merge refusal is what ends this gate
			}
			res, err := w.gate(false, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != tc.verdict {
				t.Errorf("verdict = %s (%s), want %s", res.Verdict, res.Err, tc.verdict)
			}
			if tc.errHas != "" && !strings.Contains(res.Err, tc.errHas) {
				t.Errorf("err %q lacks %q", res.Err, tc.errHas)
			}
			_, statErr := os.Stat(marker)
			if ran := statErr == nil; ran != tc.verified {
				t.Errorf("verify ran = %v, want %v", ran, tc.verified)
			}
			if asks := strings.Count(w.logText(), "PRMergeable"); asks != tc.asks {
				t.Errorf("PRMergeable asked %d times, want %d", asks, tc.asks)
			}
			if landed := strings.Contains(w.logText(), "PRRequestMerge"); landed != (tc.wantLanded || tc.opts.NoVerify || tc.verdict == GateMergeFailed && tc.verified) {
				t.Errorf("forge merge requested = %v", landed)
			}
		})
	}
}

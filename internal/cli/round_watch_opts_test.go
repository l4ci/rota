package cli

import (
	"testing"
	"time"
)

// `rota round watch` re-arms after every wake, so its waits must remember the
// permission prompts they returned (#3088). Fails if the wiring drops it.
func TestWatchWaitOptsRememberPrompts(t *testing.T) {
	o := watchWaitOpts(30*time.Second, 5*time.Second, 60, time.Minute)
	if !o.RememberPrompts {
		t.Fatal("round watch must set RememberPrompts")
	}
	if o.Timeout != 37*time.Second || o.Settle != 5*time.Second || o.Lines != 60 || o.UnverifiableAfter != time.Minute {
		t.Fatalf("%+v", o)
	}
}

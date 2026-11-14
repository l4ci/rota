package worker

import "testing"

func TestSystemTmpEnvironDropsOnlySlotTmpdir(t *testing.T) {
	root := "/p/.rota/tmp"
	for _, c := range []struct {
		tmp  string
		kept bool
	}{
		{root + "/slots/ben", false},
		{root + "/slots", false},
		{root + "/slotsx", true},
		{"/var/tmp/x", true},
	} {
		got := SystemTmpEnviron([]string{"A=1", "TMPDIR=" + c.tmp}, root)
		if kept := len(got) == 2; kept != c.kept {
			t.Errorf("TMPDIR=%s: got %v, kept=%v want %v", c.tmp, got, kept, c.kept)
		}
	}
}

package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// planProject is a pool of three slots, each with one commit: w1 and w2 write
// clash.txt differently when clash is set (so they conflict), w3 is clean.
func planProject(t *testing.T, cfg string, clash bool) string {
	t.Helper()
	dir := workerProject(t, cfg)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "3", "--base", "main")
	for _, s := range []string{"w1", "w2", "w3"} {
		wt := filepath.Join(dir, ".worktrees", s)
		file, body := s+".txt", s
		if clash && s != "w3" {
			file = "clash.txt"
		}
		write(t, filepath.Join(wt, file), body)
		gitT(t, wt, "add", file)
		gitT(t, wt, "commit", "-q", "-m", s)
	}
	return dir
}

type planJSON struct {
	Data struct {
		Verdict string `json:"verdict"`
		Changed bool   `json:"changed"`
		Plan    struct {
			Conflicts []struct {
				A     string   `json:"a"`
				B     string   `json:"b"`
				Paths []string `json:"paths"`
			} `json:"conflicts"`
			Landable     []string `json:"landable"`
			OrderBlocked []struct {
				Target        string   `json:"target"`
				ConflictsWith []string `json:"conflictsWith"`
			} `json:"orderBlocked"`
			Command  string `json:"command"`
			NoVerify bool   `json:"noVerify"`
		} `json:"plan"`
	} `json:"data"`
}

func TestWorkerTrainPlanWithConflictExitsZero(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), true)
	code, out, errOut := rotaIn(t, dir, "worker", "train", "w1", "w2", "w3", "--base", "main", "--plan")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	for _, want := range []string{"w1 x w2 (clash.txt)", "landable, in merge order: w1, w3", "order-blocked: w2 (conflicts with w1)", "run: rota worker train --base main --order w1,w3 w1 w3"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	code, out, errOut = rotaIn(t, dir, "--json", "worker", "train", "w1", "w2", "w3", "--base", "main", "--plan")
	if code != 0 {
		t.Fatalf("--json exit %d: %s %s", code, out, errOut)
	}
	var j planJSON
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	p := j.Data.Plan
	if j.Data.Verdict != "plan" || j.Data.Changed || len(p.Conflicts) != 1 || p.Conflicts[0].A != "w1" || p.Conflicts[0].B != "w2" ||
		strings.Join(p.Landable, ",") != "w1,w3" || len(p.OrderBlocked) != 1 || p.OrderBlocked[0].Target != "w2" ||
		strings.Join(p.OrderBlocked[0].ConflictsWith, ",") != "w1" || p.NoVerify {
		t.Errorf("plan data: %s", out)
	}
}

func TestWorkerTrainPlanWithoutConflictExitsZero(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), false)
	code, out, errOut := rotaIn(t, dir, "worker", "train", "w1", "w2", "w3", "--base", "main", "--plan")
	if code != 0 || !strings.Contains(out, "no member pair conflicts") || !strings.Contains(out, "landable, in merge order:") {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	code, out, _ = rotaIn(t, dir, "--json", "worker", "train", "w1", "w2", "w3", "--base", "main", "--plan")
	var j planJSON
	if err := json.Unmarshal([]byte(out), &j); code != 0 || err != nil {
		t.Fatalf("exit %d %v: %s", code, err, out)
	}
	if len(j.Data.Plan.Landable) != 3 || !strings.Contains(out, `"conflicts": []`) && !strings.Contains(out, `"conflicts":[]`) || !strings.Contains(out, `"orderBlocked"`) || strings.Contains(out, "null") {
		t.Errorf("empty lists must serialize as []: %s", out)
	}
}

func TestWorkerTrainPlanWithoutTestFullMarksNoVerify(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{}`, "off", nil), false)
	code, out, errOut := rotaIn(t, dir, "--json", "worker", "train", "w1", "w2", "w3", "--base", "main", "--plan")
	var j planJSON
	if err := json.Unmarshal([]byte(out), &j); code != 0 || err != nil {
		t.Fatalf("exit %d %v: %s %s", code, err, out, errOut)
	}
	if !j.Data.Plan.NoVerify || !strings.HasSuffix(j.Data.Plan.Command, "--no-verify") {
		t.Errorf("an empty test.full must show in the plan and its command: %s", out)
	}
}

func TestWorkerTrainPlanRefusesOrder(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), false)
	code, out, errOut := rotaIn(t, dir, "worker", "train", "w1", "w2", "--base", "main", "--plan", "--order", "w2,w1")
	if code != 2 || !strings.Contains(out+errOut, "--plan") {
		t.Errorf("exit %d: %s %s", code, out, errOut)
	}
}

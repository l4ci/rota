package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/testaudit"
)

func testAuditCommand() *Command {
	return &Command{Name: "test-audit", Summary: "report recorded test costs and test placement without running suites", Verb: testAudit}
}

func testAudit(fs *flag.FlagSet) RunFunc {
	check := fs.Bool("check", false, "read recorded thresholds only; no listing commands or checkpoint writes")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		common, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
		if err != nil {
			return Result{}, err
		}
		checkpointPath := rotastate.File(common, "test-audit.json")
		checkpoint, cerr := testaudit.ReadCheckpoint(checkpointPath)
		cfg := c.deps().Config(root)
		threshold := func(key string) (int, error) {
			v, err := config.Value(cfg, key)
			if err != nil {
				return 0, err
			}
			n, ok := jsonx.Int(v)
			if !ok || n < 0 {
				return 0, Usage("%s must be an integer, 0 or more", key)
			}
			return n, nil
		}
		seconds, err := threshold("test.audit.maxFullGateSeconds")
		if err != nil {
			return Result{}, err
		}
		prs, err := threshold("test.audit.prsBetweenAudits")
		if err != nil {
			return Result{}, err
		}
		// Gate producers use the main checkout for shared round records.
		recordRoot := root
		if main, ok := rotastate.MainCheckout(common); ok {
			recordRoot = main
		}
		started := c.deps().Now()
		telemetry := testaudit.ReadTelemetry(recordRoot, checkpoint, seconds, prs)
		if cerr != nil {
			telemetry.Complete = false
			telemetry.SuggestMaintenance = false
			telemetry.Reasons = nil
			telemetry.MergedPRsSinceAudit = nil
			telemetry.Unavailable = append(telemetry.Unavailable, "audit checkpoint unavailable: "+cerr.Error())
		}
		d := jsonx.NewObject()
		d.Set("telemetry", auditJSON(telemetry))
		d.Set("complete", telemetry.Complete)
		d.Set("checkpointRecorded", false)
		text := auditTelemetryText(telemetry)
		if *check {
			return Result{Data: d, Text: text}, nil
		}
		result, err := c.deps().Repo(root).Run(c.Context(), "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if err != nil {
			return Result{}, err
		}
		if result.ExitCode != 0 {
			return Result{}, Failed("cannot inventory repository: %s", result.Stderr)
		}
		var files []string
		seen := map[string]bool{}
		for _, f := range strings.Split(result.Stdout, "\x00") {
			if f == "" || seen[f] || auditExcludedPath(f) {
				continue
			}
			seen[f] = true
			// Tracked deletions are not present in the tree being audited.
			info, err := os.Lstat(filepath.Join(root, f))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return Result{}, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			files = append(files, f)
		}
		gc := c.deps().workerEnv().GateConfig(root)
		commands := append(append(append([]string{}, gc.Fast...), gc.Full...), gc.E2E...)
		inv := testaudit.Inventory(root, files, commands, func(name string) ([]byte, error) {
			configPath := filepath.Join(root, name)
			// Search only this repository's installed bins. Never download a runner.
			dir := filepath.Dir(configPath)
			var binary string
			for {
				candidate := filepath.Join(dir, "node_modules", ".bin", "playwright")
				if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
					binary = candidate
					break
				}
				if dir == root {
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
			if binary == "" {
				return nil, fmt.Errorf("local Playwright runner unavailable; install this repository's dependencies")
			}
			out, err := c.deps().Proc(c.Context(), proc.Cmd{Name: binary, Args: []string{"test", "--list", "--reporter=json", "--config", configPath}, Dir: filepath.Dir(configPath), Env: []string{"PLAYWRIGHT_JSON_OUTPUT_NAME=", "PLAYWRIGHT_JSON_OUTPUT_FILE="}, Group: true, MaxOutput: 16 << 20})
			if err != nil {
				return nil, err
			}
			if out.ExitCode != 0 || out.Truncated {
				return nil, fmt.Errorf("Playwright listing failed (exit %d, truncated %t): %s", out.ExitCode, out.Truncated, out.Stderr)
			}
			return []byte(out.Stdout), nil
		})
		complete := telemetry.Complete && inv.Complete
		d.Set("inventory", auditJSON(inv))
		d.Set("complete", complete)
		text += "\n" + auditInventoryText(inv)
		if complete {
			if err := testaudit.SaveCheckpoint(checkpointPath, started); err != nil {
				return Result{Data: d, Text: text}, Unavailable("audit checkpoint not saved: %v", err)
			}
			d.Set("checkpointRecorded", true)
		} else {
			text += "\nAudit incomplete; checkpoint not advanced."
		}
		return Result{Data: d, Text: text}, nil
	}
}

func auditJSON(v any) any { b, _ := json.Marshal(v); o, _ := jsonx.Decode(b); return o }
func auditExcludedPath(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		switch part {
		case ".git", ".worktrees", "node_modules", rotatree.DirName, "vendor", "dist", "build":
			return true
		}
	}
	return false
}
func auditTelemetryText(r testaudit.Telemetry) string {
	var b strings.Builder
	b.WriteString("Test audit — recorded evidence\n")
	if r.LastFullGateSeconds != nil {
		fmt.Fprintf(&b, "Latest full gate: %.3fs\n", *r.LastFullGateSeconds)
	}
	if r.MergedPRsSinceAudit != nil {
		fmt.Fprintf(&b, "Recorded merged PRs since audit: %d\n", *r.MergedPRsSinceAudit)
	}
	if r.ApprovalRecords != nil {
		fmt.Fprintf(&b, "Approval records: %d (not duration measurements)\n", *r.ApprovalRecords)
	}
	for _, s := range r.Suites {
		fmt.Fprintf(&b, "Suite %s: %d measured runs", s.Command, s.Samples)
		if s.TotalSeconds != nil {
			fmt.Fprintf(&b, ", total %.3fs, max %.3fs", *s.TotalSeconds, *s.MaxSeconds)
		} else {
			b.WriteString(", duration unavailable")
		}
		if s.Flakes != nil {
			fmt.Fprintf(&b, ", %d flakes", *s.Flakes)
		} else {
			b.WriteString(", flakes unavailable")
		}
		b.WriteByte('\n')
	}
	for _, v := range r.Unavailable {
		fmt.Fprintf(&b, "Unavailable: %s\n", v)
	}
	if r.SuggestMaintenance {
		fmt.Fprintf(&b, "Suggest a test-maintenance round: %s\n", strings.Join(r.Reasons, "; "))
	}
	b.WriteString("Assess gate shape, browser coverage and unwired suites before proposing cleanup.")
	return b.String()
}
func auditInventoryText(r testaudit.InventoryReport) string {
	var b strings.Builder
	for _, p := range r.Playwright {
		if p.Error != "" {
			fmt.Fprintf(&b, "Playwright %s: unavailable (%s)\n", p.Config, p.Error)
			continue
		}
		fmt.Fprintf(&b, "Playwright %s: %d scenarios, %d project runs\n", p.Config, p.Scenarios, p.Runs)
		names := []string{}
		for n := range p.Projects {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "  project %s: %d runs\n", n, p.Projects[n])
		}
		for _, s := range p.Specs {
			fmt.Fprintf(&b, "  spec %s: %d scenarios, %d runs\n", s.Path, s.Scenarios, s.Runs)
		}
	}
	for _, m := range r.Modules {
		fmt.Fprintf(&b, "Module candidate %s: %d test files (%s)\n", m.Module, len(m.Tests), strings.Join(m.Tests, ", "))
	}
	for _, h := range r.Helpers {
		fmt.Fprintf(&b, "Repeated helper %s: %s\n", h.Name, strings.Join(h.Paths, ", "))
	}
	for _, s := range r.UnwiredScripts {
		fmt.Fprintf(&b, "Unreferenced test script %s:%s — %s\n", s.Path, s.Name, s.Command)
	}
	for _, d := range r.DocCounts {
		actual := "unavailable"
		if d.Actual != nil {
			actual = fmt.Sprint(*d.Actual)
		}
		fmt.Fprintf(&b, "README %s: %s %d %s, observed %s (%s) %s\n", d.Path, d.Status, d.Claimed, d.Unit, actual, d.Config, d.Detail)
	}
	for _, s := range r.Diagnostics {
		fmt.Fprintf(&b, "Note: %s\n", s)
	}
	return strings.TrimSpace(b.String())
}

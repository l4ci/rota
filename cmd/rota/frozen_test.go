package main

// Frozen records (#53). The scenario suites once compared the Go binary with
// the old helpers. Before those were deleted, every scenario that passed
// recorded what the Go side did into testdata/frozen/<suite>.jsonl: the exit
// code, the --json envelope, the plain text for text scenarios, a hash of
// every .rota/ file the run changed and a hash of the fake forge's database. Go
// agreed with the oracle on each recorded scenario, so the record stands in
// for it. TestFrozen<Suite> runs the suite's scenario table and compares each
// run with its record. It needs neither bin/ nor python3, except that the
// forge suites drive test/fakes, which are Python.
//
// Regenerating: go test ./cmd/rota -run '^TestFrozen<Suite>$' -update-frozen
// accepts the current Go output, provided every scenario's own assertions
// (want, check) still pass. A change to a record is a behaviour change: read
// the jsonl diff and say why in the PR. A filtered run (-run 'TestFrozenA4/x')
// updates only the scenarios it ran and needs a file from the same day; a
// whole-suite run rewrites the file and drops records with no scenario.
//
// A suite's file holds one recording day: the fixtures commit at noon of that
// day and {dN} dates count back from it, so its git hashes and dates repeat. A
// TestFrozen run on another day re-runs its suite in a child process with the
// harness pinned to that day (ROTA_FROZEN_DAY), sets ROTA_TEST_TODAY so archive and
// stale measure age from it, and maps today's date in the output back to it.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// localDay is the local date the fixtures' {dN} tokens count back from.
var localDay = time.Now().Format("2006-01-02")

// realUTC and realLocal are the actual dates; the run under test stamps them.
var (
	realUTC   = startDay
	realLocal = localDay
)

// pinDay applies ROTA_FROZEN_DAY ("<utc>,<local>") from TestMain.
func pinDay() {
	if v := os.Getenv("ROTA_FROZEN_DAY"); v != "" {
		if utc, local, ok := strings.Cut(v, ","); ok {
			startDay, localDay = utc, local
		}
	}
}

var updateFrozen = flag.Bool("update-frozen", false, "accept the current Go output as the frozen records (see frozen_test.go)")

// frozenStep is one Go run of a scenario.
type frozenStep struct {
	Exit int               `json:"exit"`
	Env  any               `json:"env"`
	Text *string           `json:"text,omitempty"`
	Tree map[string]string `json:"tree,omitempty"` // changed .rota/ path -> content hash, or "deleted"
	DB   string            `json:"db,omitempty"`   // hash of the forge database(s) after the run
	// raw keeps what the hashes were taken of, for a readable failure.
	raw map[string]string
}

type frozenRec struct {
	Name  string       `json:"name"`
	Steps []frozenStep `json:"steps"`
}

type frozenHeader struct {
	UTC   string `json:"utc"`
	Local string `json:"local"`
}

type frozenFile struct {
	frozenHeader
	recs map[string]frozenRec
	used map[string]bool
	mu   sync.Mutex
}

func frozenPath(suite string) string {
	return filepath.Join(repoDir, "cmd", "rota", "testdata", "frozen", suite+".jsonl")
}

func loadFrozen(suite string) (*frozenFile, error) {
	f := &frozenFile{recs: map[string]frozenRec{}, used: map[string]bool{}}
	b, err := os.ReadFile(frozenPath(suite))
	if err != nil {
		return f, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 64<<20)
	for n := 0; sc.Scan(); n++ {
		if n == 0 {
			if err := json.Unmarshal(sc.Bytes(), &f.frozenHeader); err != nil {
				return f, fmt.Errorf("%s header: %v", suite, err)
			}
			continue
		}
		var r frozenRec
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return f, fmt.Errorf("%s line %d: %v", suite, n+1, err)
		}
		f.recs[r.Name] = r
	}
	return f, sc.Err()
}

func (f *frozenFile) write(suite string) error {
	names := make([]string, 0, len(f.recs))
	for n := range f.recs {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(f.frozenHeader)
	for _, n := range names {
		enc.Encode(f.recs[n])
	}
	p := frozenPath(suite)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

// suiteOf splits a subtest name into the suite file and the scenario name:
// TestFrozenA4B/list/std is a4b, list/std.
func suiteOf(t *testing.T) (suite, name string) {
	top, rest, _ := strings.Cut(t.Name(), "/")
	return strings.ToLower(strings.TrimPrefix(top, "TestFrozen")), rest
}

// ---- the frozen run --------------------------------------------------------------

var frozenOn *frozenFile

// filtered reports whether -run selects single scenarios rather than whole
// suites.
func filtered() bool {
	return strings.Contains(flag.Lookup("test.run").Value.String(), "/")
}

// runFrozen runs a suite's scenario table against its record, or with
// -update-frozen rewrites the record from it.
func runFrozen(t *testing.T, suite func(*testing.T)) {
	name, _ := suiteOf(t)
	f, err := loadFrozen(name)
	if *updateFrozen {
		updateRecords(t, name, f, err, suite)
		return
	}
	if err != nil {
		t.Fatalf("no frozen record for %s: %v (go test -run '^%s$' -update-frozen writes one)", name, err, t.Name())
	}
	if f.UTC != startDay || f.Local != localDay {
		if os.Getenv("ROTA_FROZEN_DAY") != "" {
			t.Fatalf("pinned day %s,%s but %s was recorded on %s,%s", startDay, localDay, name, f.UTC, f.Local)
		}
		reexec(t, f.frozenHeader)
		return
	}
	frozenOn = f
	t.Cleanup(func() {
		frozenOn = nil
		if t.Failed() || filtered() {
			return
		}
		var stale []string
		for n := range f.recs {
			if !f.used[n] {
				stale = append(stale, n)
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			t.Errorf("%d records have no scenario (renamed or removed? run with -update-frozen): %v", len(stale), stale)
		}
	})
	suite(t)
}

// updateRecords runs the suite today and writes what it did as the record. A
// whole-suite run replaces the file; a filtered one merges into a file from
// the same day, since one file holds one fixture day.
func updateRecords(t *testing.T, name string, old *frozenFile, loadErr error, suite func(*testing.T)) {
	if loadErr != nil && !os.IsNotExist(loadErr) {
		t.Fatal(loadErr)
	}
	hdr := frozenHeader{startDay, localDay}
	f := &frozenFile{frozenHeader: hdr, recs: map[string]frozenRec{}, used: map[string]bool{}}
	if filtered() {
		if old.frozenHeader != hdr {
			t.Fatalf("%s was recorded on %s,%s: a filtered -update-frozen cannot mix days; update the whole suite", name, old.UTC, old.Local)
		}
		f.recs = old.recs
	}
	frozenOn = f
	t.Cleanup(func() {
		frozenOn = nil
		if t.Failed() {
			t.Logf("%s: not written, the run failed", frozenPath(name))
			return
		}
		if err := f.write(name); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d records", frozenPath(name), len(f.recs))
	})
	suite(t)
}

// reexec runs the one TestFrozen function in a child process pinned to the
// recording day.
func reexec(t *testing.T, day frozenHeader) {
	t.Helper()
	args := []string{"-test.run=^" + t.Name() + "$", "-test.count=1", "-test.timeout=30m"}
	if testing.Verbose() {
		args = append(args, "-test.v")
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "ROTA_FROZEN_DAY="+day.UTC+","+day.Local)
	out, err := cmd.CombinedOutput()
	if err != nil || testing.Verbose() {
		t.Logf("pinned to %s,%s:\n%s", day.UTC, day.Local, out)
	}
	if err != nil {
		t.Fatalf("frozen run pinned to %s: %v", day.UTC, err)
	}
}

// frozenCheck compares a scenario's Go runs with its record, or with
// -update-frozen stores them as the record.
func frozenCheck(t *testing.T, got frozenRec) {
	t.Helper()
	_, name := suiteOf(t)
	f := frozenOn
	if *updateFrozen {
		if t.Failed() {
			return
		}
		got.Name = name
		f.mu.Lock()
		f.recs[name] = got
		f.mu.Unlock()
		return
	}
	f.mu.Lock()
	want, ok := f.recs[name]
	f.used[name] = true
	f.mu.Unlock()
	if !ok {
		t.Fatalf("scenario %q is not frozen (run with -update-frozen to record it)", name)
	}
	if len(got.Steps) != len(want.Steps) {
		t.Fatalf("%d runs, record has %d", len(got.Steps), len(want.Steps))
	}
	for i, g := range got.Steps {
		w := want.Steps[i]
		tag := fmt.Sprintf("run %d", i+1)
		if g.Exit != w.Exit {
			t.Errorf("%s: exit = %d, frozen %d", tag, g.Exit, w.Exit)
		}
		if !reflect.DeepEqual(g.Env, w.Env) {
			gj, _ := json.Marshal(g.Env)
			wj, _ := json.Marshal(w.Env)
			t.Errorf("%s: envelope differs\nfrozen: %s\ngo:     %s", tag, wj, gj)
		}
		if !reflect.DeepEqual(g.Text, w.Text) {
			t.Errorf("%s: text differs\nfrozen: %q\ngo:     %q", tag, deref(w.Text), deref(g.Text))
		}
		for _, p := range unionKeys(g.Tree, w.Tree) {
			if g.Tree[p] != w.Tree[p] {
				t.Errorf("%s: %s: %s, frozen %s; go content:\n%s", tag, p, orNone(g.Tree[p]), orNone(w.Tree[p]), g.raw[p])
			}
		}
		if g.DB != w.DB {
			t.Errorf("%s: forge database %s, frozen %s; go database:\n%s", tag, g.DB, w.DB, g.raw["\x00db"])
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func orNone(s string) string {
	if s == "" {
		return "unchanged"
	}
	return s
}

func unionKeys(a, b map[string]string) []string {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- building a record -------------------------------------------------------

// frozenEnv pins the day archive and stale measure age from; a scenario's own
// ROTA_TEST_TODAY comes later and wins.
func frozenEnv(env []string) []string {
	return append([]string{"ROTA_TEST_TODAY=" + localDay}, env...)
}

var (
	stampRe   = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)?`)
	compactRe = regexp.MustCompile(`\b\d{8}T\d{6}\b`)
)

// normFrozen makes one run's output independent of when and where it ran:
// temp dirs become <T>, <TP> and <TMP>, today's date becomes the recording day, and
// timestamps on or after the recording day become <TS>.
func normFrozen(s string) string {
	if harnessTmp != "" {
		tmp := regexp.QuoteMeta(filepath.Join(harnessTmp, "tmp"))
		s = regexp.MustCompile(tmp+`/[^/"\s]+(/\d{3})?`).ReplaceAllStringFunc(s, func(m string) string {
			if strings.Count(m[len(harnessTmp):], "/") == 3 {
				return "<T>" // a t.TempDir()
			}
			return "<TP>" // the test's temp root, the parent of its t.TempDir()s
		})
		s = strings.ReplaceAll(s, harnessTmp, "<TMP>")
	}
	if realUTC != startDay {
		s = strings.ReplaceAll(s, realUTC, startDay)
	}
	if realLocal != localDay {
		s = strings.ReplaceAll(s, realLocal, localDay)
	}
	s = stampRe.ReplaceAllStringFunc(s, func(m string) string {
		if m[:10] >= startDay {
			return "<TS>"
		}
		return m
	})
	return compactRe.ReplaceAllString(s, "<TSC>")
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// newStep records one Go run: before is the fixture's .rota/ tree, dir the
// copy the run changed, db the forge database(s) after it (nil for none).
func newStep(t *testing.T, r run, before map[string]string, dir string, db any) frozenStep {
	t.Helper()
	st := frozenStep{Exit: r.code, raw: map[string]string{}}
	if err := json.Unmarshal([]byte(normFrozen(r.stdout)), &st.Env); err != nil {
		t.Fatalf("go stdout is not one JSON document: %v\nstdout: %q\nstderr: %s", err, r.stdout, r.stderr)
	}
	after := snapshot(t, dir)
	for _, p := range unionKeys(before, after) {
		b, inB := before[p]
		a, inA := after[p]
		switch {
		case !inA:
			st.put(p, "deleted", "")
		case !inB || a != b:
			n := normFrozen(a)
			st.put(p, hashOf(n), n)
		}
	}
	if db != nil && !reflect.ValueOf(db).IsNil() {
		raw, _ := json.MarshalIndent(db, "", " ")
		n := normFrozen(string(raw))
		st.DB = hashOf(n)
		st.raw["\x00db"] = n
	}
	return st
}

func (st *frozenStep) put(path, hash, content string) {
	if st.Tree == nil {
		st.Tree = map[string]string{}
	}
	st.Tree[path] = hash
	st.raw[path] = content
}

func (st *frozenStep) text(stdout string) {
	n := normFrozen(stdout)
	st.Text = &n
}

func withoutJSON(argv []string) []string {
	var out []string
	for _, a := range argv {
		if a != "--json" {
			out = append(out, a)
		}
	}
	return out
}

func TestFrozenA4(t *testing.T)             { runFrozen(t, suiteA4) }
func TestFrozenA4B(t *testing.T)            { runFrozen(t, suiteA4B) }
func TestFrozenA4C(t *testing.T)            { runFrozen(t, suiteA4C) }
func TestFrozenA4D(t *testing.T)            { runFrozen(t, suiteA4D) }
func TestFrozenA4Issue(t *testing.T)        { runFrozen(t, suiteA4Issue) }
func TestFrozenA4Umbrella(t *testing.T)     { runFrozen(t, suiteA4Umbrella) }
func TestFrozenA4UmbrellaFile(t *testing.T) { runFrozen(t, suiteA4UmbrellaFile) }

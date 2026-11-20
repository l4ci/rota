package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
)

// Exercise the file lifecycle through both public migration interfaces, with
// real config layers and their filesystem locks.
func TestMigrationFiles(t *testing.T) {
	for _, step := range []struct {
		name, old, migrated, conflict string
		run                           func(string, string, bool) ([]string, error)
		checkConflict                 func(*testing.T, error)
	}{
		{"accounts", `{"round":{"codexAccounts":[{"name":"c","codexHome":"/c"}]}}`,
			`{"round":{"accounts":[{"kind":"codex","name":"c","dir":"/c"}]}}`,
			`{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}],"opencodeAccounts":[{"name":"x","configDir":"/o"}]}}`,
			func(root, global string, apply bool) ([]string, error) {
				m, err := MigrateAccountsFiles(root, global, apply)
				var files []string
				for _, c := range m.Changes {
					files = append(files, c.File)
				}
				return files, err
			},
			func(t *testing.T, err error) {
				var ce *AccountConflictError
				want := []AccountConflict{{File: "config.local.json", Name: "x", A: "/c", B: "/o"}}
				if !errors.As(err, &ce) || !reflect.DeepEqual(ce.Conflicts, want) {
					t.Fatalf("conflict = %v, want %+v", err, want)
				}
			}},
		{"merges", `{"models":{"orchestrator":"sonnet"}}`, `{"orchestrator":{"model":"sonnet"}}`,
			`{"round":{"codexCommand":"a","commands":{"codex":"b"}}}`,
			func(root, global string, apply bool) ([]string, error) {
				m, err := MigrateMergesFiles(root, global, apply)
				var files []string
				for _, c := range m.Changes {
					files = append(files, c.File)
				}
				return files, err
			},
			func(t *testing.T, err error) {
				var ce *MergeConflictError
				want := []MergeConflict{{File: "config.local.json", Old: "round.codexCommand", New: "round.commands.codex"}}
				if !errors.As(err, &ce) || !reflect.DeepEqual(ce.Conflicts, want) {
					t.Fatalf("conflict = %v, want %+v", err, want)
				}
			}},
	} {
		t.Run(step.name, func(t *testing.T) {
			paths := func(r acctRig) []string { return []string{GlobalPath(r.gdir), r.cfg(), r.local()} }
			assertBytes := func(t *testing.T, r acctRig, want []string) {
				t.Helper()
				for i, path := range paths(r) {
					if got := readFile(t, path); got != want[i] {
						t.Errorf("%s bytes = %q, want %q", path, got, want[i])
					}
				}
			}
			t.Run("preview apply and idempotence", func(t *testing.T) {
				r := newAcctRig(t, step.old, step.old, step.old)
				wantFiles := []string{"global config.json", "config.json", "config.local.json"}
				files, err := step.run(r.root, r.gdir, false)
				if err != nil || !reflect.DeepEqual(files, wantFiles) {
					t.Fatalf("preview = %v, %v", files, err)
				}
				assertBytes(t, r, []string{step.old, step.old, step.old})
				files, err = step.run(r.root, r.gdir, true)
				if err != nil || !reflect.DeepEqual(files, wantFiles) {
					t.Fatalf("apply = %v, %v", files, err)
				}
				var written []string
				for _, path := range paths(r) {
					if got := r.read(t, path); got != compact(t, decodeCfg(t, step.migrated)) {
						t.Errorf("%s = %s, want %s", path, got, step.migrated)
					}
					written = append(written, readFile(t, path))
				}
				if files, err := step.run(r.root, r.gdir, true); err != nil || len(files) != 0 {
					t.Fatalf("second apply = %v, %v", files, err)
				}
				assertBytes(t, r, written)
			})
			t.Run("unchanged layer keeps its bytes", func(t *testing.T) {
				const unchanged = "{ \"round\": {\"slots\": 2} }\n"
				r := newAcctRig(t, step.old, unchanged, step.old)
				files, err := step.run(r.root, r.gdir, true)
				if err != nil || !reflect.DeepEqual(files, []string{"global config.json", "config.local.json"}) {
					t.Fatalf("apply = %v, %v", files, err)
				}
				if got := readFile(t, r.cfg()); got != unchanged {
					t.Fatalf("unchanged layer bytes = %q", got)
				}
			})
			t.Run("late conflict refuses all writes", func(t *testing.T) {
				r := newAcctRig(t, step.old, step.old, step.conflict)
				files, err := step.run(r.root, r.gdir, true)
				step.checkConflict(t, err)
				if len(files) != 0 || !strings.Contains(err.Error(), "nothing written") {
					t.Fatalf("refusal = %v, %v", files, err)
				}
				assertBytes(t, r, []string{step.old, step.old, step.conflict})
			})
			for _, layer := range []string{"config.json", "config.local.json"} {
				for _, bad := range []string{`{"broken":`, `[]`, " \n"} {
					if layer == "config.local.json" && bad == " \n" {
						continue
					}
					t.Run("corrupt "+layer+" "+bad, func(t *testing.T) {
						bodies := []string{step.old, step.old, step.old}
						if layer == "config.json" {
							bodies[1] = bad
						} else {
							bodies[2] = bad
						}
						r := newAcctRig(t, bodies[0], bodies[1], bodies[2])
						files, err := step.run(r.root, r.gdir, true)
						if len(files) != 0 || !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), ".rota/"+layer) {
							t.Fatalf("corrupt = %v, %v", files, err)
						}
						assertBytes(t, r, bodies)
					})
				}
				t.Run("unreadable "+layer, func(t *testing.T) {
					r := newAcctRig(t, step.old, step.old, step.old)
					path := r.cfg()
					if layer == "config.local.json" {
						path = r.local()
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0755); err != nil {
						t.Fatal(err)
					}
					files, err := step.run(r.root, r.gdir, true)
					if len(files) != 0 || err == nil || errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), ".rota/"+layer) {
						t.Fatalf("read error = %v, %v", files, err)
					}
					if got := readFile(t, GlobalPath(r.gdir)); got != step.old {
						t.Fatal("global written before read error")
					}
				})
			}
			for _, global := range []string{"missing", "directory", `{"broken":`, `[]`, " \n"} {
				t.Run("skip global "+global, func(t *testing.T) {
					body := global
					if global == "missing" || global == "directory" {
						body = ""
					}
					r := newAcctRig(t, body, step.old, "")
					if global == "directory" {
						if err := os.Mkdir(GlobalPath(r.gdir), 0755); err != nil {
							t.Fatal(err)
						}
					}
					files, err := step.run(r.root, r.gdir, true)
					if err != nil || !reflect.DeepEqual(files, []string{"config.json"}) {
						t.Fatalf("apply = %v, %v", files, err)
					}
					if body != "" && readFile(t, GlobalPath(r.gdir)) != body {
						t.Fatal("unusable global rewritten")
					}
					if got := r.read(t, r.cfg()); got != compact(t, decodeCfg(t, step.migrated)) {
						t.Fatalf("project = %s", got)
					}
				})
			}
			for _, local := range []string{"", " \n\t"} {
				t.Run("absent or blank local "+local, func(t *testing.T) {
					r := newAcctRig(t, "", step.old, local)
					files, err := step.run(r.root, "", true)
					if err != nil || !reflect.DeepEqual(files, []string{"config.json"}) {
						t.Fatalf("apply = %v, %v", files, err)
					}
					if local == "" {
						if _, err := os.Stat(r.local()); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("missing local created: %v", err)
						}
					} else if readFile(t, r.local()) != local {
						t.Fatal("blank local rewritten")
					}
				})
			}
			t.Run("missing project", func(t *testing.T) {
				r := newAcctRig(t, "", "", step.old)
				files, err := step.run(r.root, "", true)
				if err != nil || !reflect.DeepEqual(files, []string{"config.local.json"}) {
					t.Fatalf("apply = %v, %v", files, err)
				}
				if _, err := os.Stat(r.cfg()); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing project created: %v", err)
				}
			})
		})
	}
}

// A refusal in any layer-file step leaves every file untouched, even the files
// an earlier step would have rewritten: Migrate and Fill plan every step
// before applying one.
func TestRefusalLeavesEveryFileUntouched(t *testing.T) {
	renamable := `{"work":{"dispatch":"tmux"}}`
	cases := map[string]struct {
		local string
		want  func(error) bool
	}{
		"round": {`{"work":{"workerSlots":2},"round":{"slots":4}}`, func(err error) bool { var e *ConflictError; return errors.As(err, &e) }},
		"accounts": {`{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}],"opencodeAccounts":[{"name":"x","configDir":"/o"}]}}`,
			func(err error) bool { var e *AccountConflictError; return errors.As(err, &e) }},
		"merges": {`{"models":{"orchestrator":"sonnet"},"orchestrator":{"model":"opus"}}`,
			func(err error) bool { var e *MergeConflictError; return errors.As(err, &e) }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newAcctRig(t, "", renamable, tc.local)
			before := map[string]string{}
			for _, p := range []string{r.cfg(), localPath(r.root)} {
				before[p] = readFile(t, p)
			}
			_, err := Migrate(r.env, r.root, true, nil)
			if !tc.want(err) {
				t.Fatalf("Migrate err = %v, want the %s refusal", err, name)
			}
			if _, err := Fill(r.env, r.root); !tc.want(err) {
				t.Fatalf("Fill err = %v, want the %s refusal", err, name)
			}
			for p, want := range before {
				if got := readFile(t, p); got != want {
					t.Errorf("%s changed:\n%s", filepath.Base(p), got)
				}
			}
		})
	}
}

// The autonomy step's stale check runs under the global config's lock, as the
// lifecycle runs it: a set that stamps the global while the step waits for the
// lock leaves its deliberate auto alone, and the step stamps nothing over it.
func TestAutonomyGlobalStepRechecksStampUnderLock(t *testing.T) {
	gdir := t.TempDir()
	gpath := GlobalPath(gdir)
	if err := os.WriteFile(gpath, []byte(`{"autonomy": {"level": "auto"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := layerFiles(project(t, `{}`, ""), gdir, false)
	done := make(chan error, 1)
	err := fsio.Locked(gpath, fsio.LockTimeout, func() error {
		go func() {
			done <- migrateLayerFiles(files, true, func([]*migrationFile) error {
				planAutonomyGlobal(files[0], gdir)
				return nil
			})
		}()
		time.Sleep(100 * time.Millisecond) // the step is now queued on the lock
		return fsio.WriteFileAtomic(globalStampPath(gdir), []byte(AutonomyLevelsSince+"\n"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gpath); !strings.Contains(string(b), `"auto"`) {
		t.Errorf("a concurrently stamped global auto was rewritten: %s", b)
	}
}

// With a stale stamp the same step rewrites the auto and stamps the global,
// the stamp landing after the document.
func TestAutonomyGlobalStepRewritesAndStamps(t *testing.T) {
	gdir := t.TempDir()
	gpath := GlobalPath(gdir)
	os.WriteFile(gpath, []byte(`{"autonomy": {"level": "auto"}}`), 0o644)
	files := layerFiles(project(t, `{}`, ""), gdir, false)
	if err := migrateLayerFiles(files, false, func([]*migrationFile) error {
		planAutonomyGlobal(files[0], gdir)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gpath); !strings.Contains(string(b), `"auto"`) || globalStale(gdir) == false {
		t.Fatalf("a preview wrote: %s, stale=%v", b, globalStale(gdir))
	}
	if err := migrateLayerFiles(files, true, func([]*migrationFile) error {
		planAutonomyGlobal(files[0], gdir)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gpath); !strings.Contains(string(b), `"roundtrip"`) || globalStale(gdir) {
		t.Fatalf("global not rewritten and stamped: %s, stale=%v", b, globalStale(gdir))
	}
}

// `rota migrate config` runs the round, accounts, merges and scope steps and
// nothing else. The autonomy rewrite and its stamp belong to `config fill`,
// whose once-guard they are, so a stale auto survives a migrate untouched.
func TestMigrateConfigStepList(t *testing.T) {
	var got []string
	mt := reflect.TypeOf(Migration{})
	for i := 0; i < mt.NumField(); i++ {
		got = append(got, mt.Field(i).Name)
	}
	if want := []string{"Round", "Accounts", "Merges", "Scope"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("migrate config steps = %v, want %v", got, want)
	}
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
	gdir := t.TempDir()
	os.WriteFile(GlobalPath(gdir), []byte(`{"autonomy": {"level": "auto"}}`), 0o644)
	before := []string{read(t, root), readLocal(t, root), string(readFileOr(GlobalPath(gdir)))}
	if _, err := Migrate(dirEnv(t, gdir), root, true, nil); err != nil {
		t.Fatal(err)
	}
	after := []string{read(t, root), readLocal(t, root), string(readFileOr(GlobalPath(gdir)))}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("migrate config ran the autonomy step:\nbefore %q\nafter  %q", before, after)
	}
	if _, err := os.Stat(globalStampPath(gdir)); err == nil {
		t.Error("migrate config stamped the global")
	}
}

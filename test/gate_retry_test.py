"""Run the real gate with disposable, controlled checks (never the full suite)."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


class GateRetryTest(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.repo = self.root / "repo"
        (self.repo / "test/sections").mkdir(parents=True)
        source = Path(__file__).resolve().parent
        shutil.copy(os.environ.get("GATE_SCRIPT", source / "gate.sh"), self.repo / "test/gate.sh")
        shutil.copytree(source / "lib", self.repo / "test/lib")
        for helper in source.glob("gate-*.py"):
            shutil.copy(helper, self.repo / "test" / helper.name)
        (self.repo / "test/sections/01_stub.sh").touch()
        (self.repo / "go.mod").write_text("module github.com/l4ci/rota\n")
        (self.repo / "test/runner.sh").write_text(
            'exec python3 "$CONTROL/check.py" smoke-shard1 "$@"\n')
        self.control = self.root / "control"
        self.control.mkdir()
        (self.control / "check.py").write_text('''
import json, os, pathlib, sys, time
root = pathlib.Path(os.environ["CONTROL"])
name = sys.argv[1]
if name == "go" and sys.argv[2] == "vet":
    name = "vet"
elif name == "go":
    name = "go:internal/example"
count = root / (name.replace("/", "_") + ".count")
n = int(count.read_text()) + 1 if count.exists() else 1
count.write_text(str(n))
with (root / "calls").open("a") as f:
    f.write(json.dumps(dict(name=name, n=n, cwd=os.getcwd(), args=sys.argv[2:])) + "\\n")
active = root / (name.replace("/", "_") + ".active")
if n > 1 and list(root.glob("*.active")):
    raise SystemExit("retry overlapped initial checks")
active.touch()
# Keep a later initial check alive after the failing check exits, so the
# isolation assertion cannot pass merely because every stub finished quickly.
slow = (name == "smoke-shard1" and os.environ.get("FAIL_CHECK") == "go:internal/example") or (name == "go:internal/example" and os.environ.get("FAIL_CHECK") == "smoke-shard1")
time.sleep(0.4 if n == 1 and slow else 0.04)
active.unlink()
mode = os.environ.get("FAIL_CHECK", "")
failed = name == mode and (n == 1 or os.environ.get("FAIL_TWICE") == "1")
if failed and os.environ.get("DIRTY_ON_FAIL") == "1":
    pathlib.Path("go.mod").write_text("module changed\\n")
if name.startswith("go:"):
    pkg = "github.com/l4ci/rota/internal/example"
    if failed and os.environ.get("GO_TIMEOUT") == "1":
        print(json.dumps(dict(Action="output", Package=pkg, Output="panic: test timed out after 30m")))
    if failed and os.environ.get("GO_INFRA"):
        print(json.dumps(dict(Action="output", Package=pkg, Output=os.environ["GO_INFRA"])))
    output = "--- FAIL: TestUnderLoad (0s)\\nFAIL\\n" if failed else "ok example/pkg\\n"
    print(json.dumps(dict(Action="output", Package=pkg, Output=output)))
    print(json.dumps(dict(Action="fail" if failed else "pass", Package=pkg)))
elif name.startswith("smoke-") and not failed and os.environ.get("OMIT_SUMMARY") != "1":
    print("All smoke tests passed.")
else:
    print("controlled " + name)
raise SystemExit(int(os.environ.get("FAIL_EXIT", "1")) if failed else 0)
''')
        for name in ("doclint", "grep-gate", "gofmt"):
            (self.repo / "test" / (name + ".sh")).write_text(
                'exec python3 "$CONTROL/check.py" ' + name.replace("grep-gate", "grepgate") + '\n')
        (self.repo / "test/validate-skills.py").write_text(
            'import os, runpy, sys\nsys.argv = ["check", "validate"]\n'
            'runpy.run_path(os.environ["CONTROL"] + "/check.py", run_name="__main__")\n')
        bindir = self.root / "bin"
        bindir.mkdir()
        go = bindir / "go"
        go.write_text('#!/bin/sh\nexec python3 "$CONTROL/check.py" go "$@"\n')
        go.chmod(0o755)
        self.env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
                        CONTROL=str(self.control), GOCACHE=str(self.root / "cache"),
                        ROTA_TEST_LOCK_HELD="1", ROTA_GATE_LOCK=str(self.root / "gate.lock"),
                        ROTA_GATE_LOGS=str(self.root / "logs"))
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        subprocess.run(["git", "-C", str(self.repo), "add", "--", "test", "go.mod"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "-c", "user.name=test", "-c",
                        "user.email=test@example.com", "commit", "-qm", "seed"], check=True)

    def run_gate(self, check, twice=False):
        result = subprocess.run(["bash", "test/gate.sh", "--shards", "1"],
                                cwd=self.repo, env=dict(self.env, FAIL_CHECK=check,
                                FAIL_TWICE=str(int(twice))), text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
        calls = [json.loads(line) for line in (self.control / "calls").read_text().splitlines()]
        return result, calls

    def test_go_timeout_is_not_retried(self):
        self.env["GO_TIMEOUT"] = "1"
        result, calls = self.run_gate("go:internal/example")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertEqual(max(c["n"] for c in calls), 1, calls)
        self.assertNotIn("FLAKY", result.stdout)

    def test_go_infrastructure_failure_is_not_retried(self):
        self.env["GO_INFRA"] = "fork/exec compiler: resource temporarily unavailable"
        result, calls = self.run_gate("go:internal/example")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertEqual(max(c["n"] for c in calls), 1, calls)
        self.assertNotIn("FLAKY", result.stdout)

    def test_smoke_infrastructure_exit_is_not_retried(self):
        self.env["FAIL_EXIT"] = "2"
        result, calls = self.run_gate("smoke-shard1")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertEqual(max(c["n"] for c in calls), 1, calls)
        self.assertNotIn("FLAKY", result.stdout)

    def test_recovery_reports_both_run_outcomes_for_the_ledger(self):
        result, calls = self.run_gate("smoke-shard1")
        self.assertEqual(result.returncode, 0, result.stdout)
        records = [json.loads(line.removeprefix("ROTA-FLAKY "))
                   for line in result.stdout.splitlines() if line.startswith("ROTA-FLAKY ")]
        self.assertEqual(len(records), 1, result.stdout)
        record = records[0]
        self.assertEqual(record["check"], "smoke-shard1")
        self.assertEqual((record["firstExit"], record["retryExit"]), (1, 0))
        self.assertGreater(record.get("retrySeconds", 0), 0, record)
        self.assertIn("controlled smoke-shard1", record["firstOutput"])
        self.assertIn("All smoke tests passed.", record["retryOutput"])

    def test_incomplete_smoke_success_is_not_retried(self):
        self.env["OMIT_SUMMARY"] = "1"
        result, calls = self.run_gate("")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertEqual(max(c["n"] for c in calls), 1, calls)
        self.assertNotIn("FLAKY", result.stdout)

    def test_changed_tree_is_not_retried(self):
        self.env["DIRTY_ON_FAIL"] = "1"
        result, calls = self.run_gate("smoke-shard1")
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertEqual(max(c["n"] for c in calls), 1, calls)
        self.assertNotIn("FLAKY", result.stdout)

    def test_persistent_failures_stay_red_after_exactly_two_runs(self):
        for check in ("go:internal/example", "smoke-shard1"):
            with self.subTest(check=check):
                # Each gate invocation owns an independent set of counters.
                for path in self.control.glob("*.count"):
                    path.unlink()
                (self.control / "calls").unlink(missing_ok=True)
                result, calls = self.run_gate(check, twice=True)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertEqual(max(c["n"] for c in calls if c["name"] == check), 2)
                self.assertNotIn("FLAKY", result.stdout)

    def test_deterministic_checks_are_never_retried(self):
        for check in ("gofmt", "vet", "doclint", "grepgate", "validate"):
            with self.subTest(check=check):
                for path in self.control.glob("*.count"):
                    path.unlink()
                (self.control / "calls").unlink(missing_ok=True)
                result, calls = self.run_gate(check)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertEqual(max(c["n"] for c in calls), 1, calls)
                self.assertNotIn("FLAKY", result.stdout)

    def test_flaky_go_retries_only_the_failed_package_without_cache(self):
        result, calls = self.run_gate("go:internal/example")
        self.assertEqual(result.returncode, 0, result.stdout)
        retries = [c for c in calls if c["n"] > 1]
        self.assertEqual(len(retries), 1, calls)
        self.assertEqual(retries[0]["args"][-1], "github.com/l4ci/rota/internal/example")
        self.assertIn("-count=1", retries[0]["args"])
        self.assertIn("FLAKY go:internal/example", result.stdout)
        records = [json.loads(line.removeprefix("ROTA-FLAKY "))
                   for line in result.stdout.splitlines() if line.startswith("ROTA-FLAKY ")]
        self.assertEqual(len(records), 1, result.stdout)
        self.assertEqual(records[0]["check"], "go:internal/example")
        self.assertIn("TestUnderLoad", records[0]["firstOutput"])
        self.assertIn("ok example/pkg", records[0]["retryOutput"])

    def test_flaky_smoke_retries_once_after_all_initial_checks(self):
        result, calls = self.run_gate("smoke-shard1")
        self.assertEqual(result.returncode, 0, result.stdout)
        counts = {c["name"]: c["n"] for c in calls}
        self.assertEqual(counts["smoke-shard1"], 2, calls)
        self.assertTrue(all(n == 1 for name, n in counts.items() if name != "smoke-shard1"), calls)
        self.assertIn("FLAKY", result.stdout)
        self.assertTrue(all(c["cwd"] == str(self.repo) for c in calls))


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--measure":
        os.environ["GATE_SCRIPT"] = sys.argv[2]
        rows = []
        for check, twice in [("go:internal/example", False), ("smoke-shard1", False),
                             ("go:internal/example", True), ("smoke-shard1", True),
                             ("gofmt", False), ("vet", False), ("doclint", False),
                             ("grepgate", False)]:
            fixture = GateRetryTest()
            try:
                fixture.setUp()
                result, calls = fixture.run_gate(check, twice)
                rows.append(dict(check=check, persistent=twice, gateExit=result.returncode,
                                 runs=max(c["n"] for c in calls if c["name"] == check)))
            finally:
                fixture.doCleanups()
        print(json.dumps(rows, indent=2))
    else:
        unittest.main()

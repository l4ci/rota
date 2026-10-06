"""Exercise the real runner against disposable checkouts, never the dev tree."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest


class RunnerLeakTest(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        source = Path(__file__).resolve().parent
        (self.repo / "test/sections").mkdir(parents=True)
        shutil.copy(source / "runner.sh", self.repo / "test/runner.sh")
        shutil.copy(source / "gate.sh", self.repo / "test/gate.sh")
        shutil.copy(source / "lib.sh", self.repo / "test/lib.sh")
        shutil.copytree(source / "lib", self.repo / "test/lib")
        (self.repo / ".rota").mkdir()
        self.paths = ["CLAUDE.md", "AGENTS.md", ".rota/KNOWLEDGE.md"]
        for path in self.paths:
            (self.repo / path).write_bytes(b"original\n")
        self.git("init", "-q")
        self.git("add", ".")
        self.git("commit", "-qm", "seed")
        (self.repo / "test/sections/01_wait.sh").write_text('''
if [ "${INJECT_LEAK:-0}" = 1 ]; then
  printf 'test leak\\n' > "$REPO/AGENTS.md"
fi
python3 - "$CONTROL" <<'PY'
from pathlib import Path
import sys, time
control = Path(sys.argv[1])
(control / "ready").touch()
deadline = time.monotonic() + 30
while not (control / "release").exists():
    if time.monotonic() > deadline:
        raise SystemExit("timed out waiting for test release")
    time.sleep(0.02)
PY
[ "${SECTION_FAIL:-0}" = 0 ] || exit 27
''')

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-C", str(self.repo), "-c", "user.name=test", "-c",
             "user.email=test@example.com", *args], stderr=subprocess.STDOUT)

    def start(self, name="one", leak=False, gate=False, section_fail=False):
        control = self.root / name
        control.mkdir()
        env = dict(os.environ, ROTA_BIN="/bin/true", CONTROL=str(control),
                   SECTION_LIST="test/sections/01_wait.sh",
                   ROTA_SMOKE_DIAGNOSTICS=str(self.root / "diagnostics"),
                   TMPDIR=str(self.root), INJECT_LEAK=str(int(leak)),
                   SECTION_FAIL=str(int(section_fail)),
                   ROTA_GATE_LOGS=str(self.root / "gate-logs"),
                   ROTA_GATE_LOCK=str(self.root / "gate.lock"))
        command = ["bash", str(self.repo / "test/runner.sh")]
        if gate:
            command = ["bash", str(self.repo / "test/gate.sh"), "--smoke-only", "--shards", "1"]
        proc = subprocess.Popen(command,
                                env=env, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, text=True)
        def cleanup():
            (control / "release").touch()
            if proc.poll() is None:
                proc.wait(timeout=30)
            proc.communicate()
        self.addCleanup(cleanup)
        deadline = time.monotonic() + 30
        while not (control / "ready").exists():
            if proc.poll() is not None:
                self.fail("runner stopped before section: " + proc.communicate()[0])
            if time.monotonic() > deadline:
                self.fail("runner did not reach section")
            time.sleep(0.02)
        return proc, control

    def finish(self, run, expected=1):
        proc, control = run
        (control / "release").touch()
        output = proc.communicate(timeout=30)[0]
        self.assertEqual(proc.returncode, expected, output)
        if expected:
            self.assertNotIn("All smoke tests passed", output)
        return output

    def assert_diagnostics(self, output):
        self.assertIn("left untouched", output)
        line = next(line for line in output.splitlines()
                    if line.startswith("smoke diagnostics: "))
        diagnostics = Path(line.removeprefix("smoke diagnostics: "))
        self.assertTrue(diagnostics.is_dir(), output)
        self.assertIn("AGENTS.md", (diagnostics / "diff.txt").read_text())
        self.assertEqual((diagnostics / "before/AGENTS.md").read_bytes(), b"original\n")
        return diagnostics

    def test_concurrent_edits_and_deletions_survive(self):
        run = self.start()
        (self.repo / "AGENTS.md").write_bytes(b"concurrent\x00edit\n")
        (self.repo / "CLAUDE.md").unlink()
        (self.repo / ".rota/KNOWLEDGE.md").unlink()
        output = self.finish(run)
        self.assertEqual((self.repo / "AGENTS.md").read_bytes(), b"concurrent\x00edit\n")
        self.assertFalse((self.repo / "CLAUDE.md").exists())
        self.assertFalse((self.repo / ".rota/KNOWLEDGE.md").exists())
        diagnostics = self.assert_diagnostics(output)
        self.assertEqual((diagnostics / "after/AGENTS.md").read_bytes(), b"concurrent\x00edit\n")

    def test_overlapping_shards_preserve_a_merge(self):
        first = self.start("first")
        (self.repo / "AGENTS.md").write_text("between snapshots\n")
        second = self.start("second")
        self.git("checkout", "-qb", "incoming")
        for path in self.paths:
            (self.repo / path).write_text("merged\n")
        self.git("add", *self.paths)
        self.git("commit", "-qm", "incoming change")
        self.git("checkout", "-q", "-")
        self.git("merge", "--ff-only", "incoming")
        head = self.git("rev-parse", "HEAD")
        outputs = []
        for run in (first, second):
            outputs.append(self.finish(run))
            for path in self.paths:
                self.assertEqual((self.repo / path).read_bytes(), b"merged\n")
            self.assertEqual(self.git("rev-parse", "HEAD"), head)
        first_diag = self.assert_diagnostics(outputs[0])
        second_diag = Path(next(line.removeprefix("smoke diagnostics: ")
                                for line in outputs[1].splitlines()
                                if line.startswith("smoke diagnostics: ")))
        self.assertNotEqual(first_diag, second_diag)
        self.assertEqual((second_diag / "before/AGENTS.md").read_bytes(), b"between snapshots\n")

    def test_test_leak_fails_without_overwriting_later_work(self):
        run = self.start(leak=True)
        self.assertEqual((self.repo / "AGENTS.md").read_bytes(), b"test leak\n")
        (self.repo / "AGENTS.md").write_bytes(b"later work\n")
        output = self.finish(run)
        self.assertEqual((self.repo / "AGENTS.md").read_bytes(), b"later work\n")
        self.assert_diagnostics(output)

    def test_failing_section_leak_keeps_gate_diagnostics(self):
        run = self.start(leak=True, gate=True, section_fail=True)
        self.finish(run)
        self.assertEqual((self.repo / "AGENTS.md").read_bytes(), b"test leak\n")
        logs = self.root / "gate-logs"
        diagnostics = self.assert_diagnostics((logs / "smoke-shard1.log").read_text())
        self.assertEqual(diagnostics.parent, logs)
        self.assertEqual((diagnostics / "after/AGENTS.md").read_bytes(), b"test leak\n")
        self.assertFalse(list(logs.glob("tmp.*")), "gate must remove its scratch root")

    def test_initial_deletion_and_new_files(self):
        (self.repo / "AGENTS.md").unlink()
        (self.repo / ".rota/KNOWLEDGE.md").unlink()
        self.finish(self.start("clean"), expected=0)
        run = self.start("creation")
        (self.repo / "AGENTS.md").write_text("created\n")
        (self.repo / ".rota/new file.md").write_text("new tracked content\n")
        self.git("add", ".rota/new file.md")
        output = self.finish(run)
        self.assertEqual((self.repo / "AGENTS.md").read_bytes(), b"created\n")
        self.assertIn(".rota/new file.md: missing -> file", output)
        self.assertFalse((self.repo / ".rota/KNOWLEDGE.md").exists())

    def test_clean_run_passes_without_diagnostics(self):
        self.finish(self.start(), expected=0)
        self.assertFalse((self.root / "diagnostics").exists())


if __name__ == "__main__":
    unittest.main()

"""The shared sandbox policy feeds both real script entry points."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import gate_sandbox


class GateSandboxTest(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        # A dependency neither script has today, declared once, in SHARED.
        probe = mock.patch.object(gate_sandbox, "SHARED", gate_sandbox.SHARED + ("VERSION",))
        probe.start()
        self.addCleanup(probe.stop)

    def run_entry(self, repo, *command):
        for args in (["init", "-q"], ["add", "."],
                     ["-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-qm", "seed"]):
            subprocess.run(["git", "-C", str(repo), *args], check=True)
        env = dict(os.environ, ROTA_BIN="/bin/true", ROTA_TEST_LOCK_HELD="1",
                   TMPDIR=str(self.root), ROTA_SMOKE_DIAGNOSTICS=str(self.root / "diagnostics"),
                   SECTION_LIST="test/sections/01_probe.sh",
                   ROTA_GATE_LOGS=str(self.root / "logs"), ROTA_GATE_LOCK=str(self.root / "gate.lock"))
        result = subprocess.run(["bash", *command], cwd=repo, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("All smoke tests passed.", result.stdout)

    def sandbox(self, name):
        repo = self.root / name
        (repo / "test/sections").mkdir(parents=True)
        return repo

    def test_gate_entry_point_gets_the_shared_dependency(self):
        repo = self.sandbox("gate")
        gate_sandbox.install_gate(repo)
        (repo / "test/sections/01_probe.sh").touch()
        (repo / "test/runner.sh").write_text(
            '[ -f VERSION ] || { echo "shared dependency missing"; exit 1; }\n'
            'echo "All smoke tests passed."\n')
        self.run_entry(repo, "test/gate.sh")

    def test_runner_entry_point_gets_the_shared_dependency(self):
        repo = self.sandbox("runner")
        gate_sandbox.install_runner(repo)
        (repo / "test/sections/01_probe.sh").write_text(
            '[ -f "$REPO/VERSION" ] || fail "shared dependency missing"\n')
        self.run_entry(repo, "test/runner.sh")


if __name__ == "__main__":
    unittest.main()

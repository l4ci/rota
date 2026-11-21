"""Run test/grep-gate.sh on a fixture repo: an old-name hit, an excluded path, a clean tree."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

GATE = Path(__file__).resolve().parent / "grep-gate.sh"


class GrepGateTest(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q")
        # A stub binary keeps the init smoke (second half of the script) out of the way.
        self.stub = self.root / "rota"
        self.stub.write_text("#!/bin/sh\nexit 0\n")
        self.stub.chmod(0o755)

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.repo, check=True)

    def add(self, name, text):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        self.git("add", name)

    def run_gate(self):
        env = {k: v for k, v in os.environ.items() if k != "ROTA_GATE_BIN"}
        env.update(GREP_GATE_REPO=str(self.repo), ROTA_BIN=str(self.stub))
        return subprocess.run(["bash", str(GATE)], env=env, capture_output=True, text=True)

    def test_clean_tree_passes(self):
        self.add("README.md", "rota only\n")
        result = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_old_product_name_fails(self):
        self.add("docs/page.md", "use hv" "-skills here\n")
        result = self.run_gate()
        self.assertEqual(result.returncode, 1)
        self.assertIn("docs/page.md", result.stderr)

    def test_old_binary_name_fails(self):
        self.add("docs/page.md", "run /hv" "-work now\n")
        result = self.run_gate()
        self.assertEqual(result.returncode, 1)
        self.assertIn("old binary name hv", result.stderr)

    def test_excluded_path_passes(self):
        self.add("CHANGELOG.md", "renamed from hv" "-skills; /hv" "-work\n")
        result = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()

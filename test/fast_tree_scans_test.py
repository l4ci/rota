"""Run the fast tier in a disposable module with the real worker-kind guard."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class FastTreeScansTest(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        source = Path(__file__).resolve().parent.parent
        for name in ("test/fast.sh", "test/gofmt.sh",
                     "internal/harness/kindswitch_test.go", "internal/testiso/nested.go"):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / name, target)
        # Before the fix the fast script never reads a scan registry. Keep the
        # same fixture usable for the RED run, without a missing-file failure.
        scanner = source / "test/treescan"
        if scanner.exists():
            shutil.copytree(scanner, self.root / "test/treescan")
        (self.root / "test/treescan").mkdir(exist_ok=True)
        (self.root / "test/treescan/registry.json").write_text(json.dumps({
            "scans": {"./internal/harness": ["TestNoKindNamedOutsideHarness"]},
            "fixtures": {"./internal/harness/TestKindGuardFindsEachPlantedShape":
                         "Only scans planted files in t.TempDir()."}
        }))
        (self.root / "go.mod").write_text("module github.com/l4ci/rota\n\ngo 1.22\n")
        (self.root / "leaf").mkdir()
        (self.root / "leaf/leaf_test.go").write_text("package leaf\n")

    def fast(self, *files, dry=False):
        return subprocess.run(
            ["bash", "test/fast.sh", *files], cwd=self.root, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120,
            env=dict(os.environ, GOWORK="off", GOPROXY="off",
                     FAST_DRY_RUN="1" if dry else "0"))

    def test_unrelated_change_runs_kind_guard(self):
        # Run the guard on a clean tree before editing a file outside its
        # package. The second invocation must inspect the new source.
        clean = self.fast("leaf/leaf_test.go")
        self.assertEqual(clean.returncode, 0, clean.stdout)
        # This is the kind of test-only comparison that escaped in #2724.
        (self.root / "leaf/leaf_test.go").write_text(
            'package leaf\n\nfunc namesKind(k string) bool { return k == "codex" }\n')
        result = self.fast("leaf/leaf_test.go")
        self.assertNotEqual(result.returncode, 0,
                            "fast tier missed a worker-kind comparison outside harness:\n" + result.stdout)
        self.assertIn("TestNoKindNamedOutsideHarness", result.stdout)
        self.assertIn("leaf/leaf_test.go", result.stdout)

    def test_empty_and_unmapped_changes_still_run_scans(self):
        for files in ((), ("notes/unmapped.txt",)):
            with self.subTest(files=files):
                result = self.fast(*files)
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertIn("fast: tree-scans", result.stdout)
                self.assertIn("github.com/l4ci/rota/internal/harness", result.stdout)

    def test_new_unregistered_scan_fails_before_package_checks(self):
        (self.root / "leaf/leaf_test.go").write_text('''package leaf

import (
    "os"
    "testing"
)

func inspectSource() { _, _ = os.ReadDir("..") }
func TestNewSourceGuard(t *testing.T) { inspectSource() }
''')
        result = self.fast("leaf/leaf_test.go")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("unregistered tree scan ./leaf/TestNewSourceGuard", result.stdout)
        self.assertNotIn("fast: go vet", result.stdout)


if __name__ == "__main__":
    unittest.main()

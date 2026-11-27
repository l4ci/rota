"""The one owner of what a disposable gate or runner sandbox needs from this
checkout (#4324). gate_retry_test.py and runner_leak_test.py build on it; a new
runtime dependency of test/gate.sh or test/runner.sh is declared here only.

Scenario setup stays in the suites: sections, stubbed checks, stubbed runner.
"""
from pathlib import Path
import shutil

SOURCE = Path(__file__).resolve().parent.parent

# Repo-relative paths or globs. SHARED is what both scripts need.
SHARED = ("test/lib",)
GATE = ("test/gate.sh", "test/gate-*.py", "test/gate-smoke-markers.txt",
        "internal/flakeledger/infrastructure-markers.txt")
RUNNER = ("test/runner.sh", "test/lib.sh", "test/lint-sections.py")


def _install(repo, patterns):
    for pattern in patterns:
        matches = list(SOURCE.glob(pattern))
        if not matches:
            raise FileNotFoundError("gate sandbox dependency matches nothing: " + pattern)
        for path in matches:
            target = repo / path.relative_to(SOURCE)
            target.parent.mkdir(parents=True, exist_ok=True)
            if path.is_dir():
                shutil.copytree(path, target, dirs_exist_ok=True)
            else:
                shutil.copy(path, target)


def install_gate(repo):
    """Copy the real gate into repo. It runs test/runner.sh: install_runner
    too, or write a stub there."""
    _install(repo, SHARED + GATE)


def install_runner(repo):
    """Copy the real smoke runner into repo, replacing any stub."""
    _install(repo, SHARED + RUNNER)

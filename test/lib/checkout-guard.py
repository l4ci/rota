"""Read-only checkout guard for smoke runs; snapshots never restore live files."""
import difflib
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def tracked_paths(repo):
    output = subprocess.check_output(
        ["git", "-C", str(repo), "ls-files", "-z", "--", ".rota/"])
    return {os.fsdecode(p) for p in output.split(b"\0") if p} | {"CLAUDE.md", "AGENTS.md"}


def snapshot(repo, destination, paths):
    destination.mkdir(parents=True)
    manifest = {}
    for name in sorted(paths):
        live = repo / name
        saved = destination / name
        try:
            if live.is_symlink():
                target = os.readlink(live)
                saved.parent.mkdir(parents=True, exist_ok=True)
                saved.symlink_to(target)
                manifest[name] = ["symlink", target]
            elif live.is_file():
                data = live.read_bytes()
                saved.parent.mkdir(parents=True, exist_ok=True)
                saved.write_bytes(data)
                manifest[name] = ["file", hashlib.sha256(data).hexdigest()]
            else:
                manifest[name] = ["other" if live.exists() else "missing"]
        except FileNotFoundError:
            # A concurrent deletion between stat and read is still a deletion.
            manifest[name] = ["missing"]
    (destination / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


def check(repo, before, diagnostics_root):
    old = json.loads((before / "manifest.json").read_text())
    # Include newly tracked files as well as paths deleted from the index.
    with tempfile.TemporaryDirectory() as scratch:
        after = Path(scratch) / "after"
        new = snapshot(repo, after, set(old) | tracked_paths(repo))
        changed = [name for name in new if old.get(name, ["missing"]) != new[name]]
        if not changed:
            return 0
        diagnostics_root.mkdir(parents=True, exist_ok=True)
        diagnostics = Path(tempfile.mkdtemp(prefix="rota-smoke-diff-", dir=diagnostics_root))
        shutil.copytree(before, diagnostics / "before", symlinks=True)
        shutil.copytree(after, diagnostics / "after", symlinks=True)
        lines = []
        for name in changed:
            previous = old.get(name, ["missing"])
            current = new[name]
            lines.append(f"{name}: {previous[0]} -> {current[0]}\n")
            if previous[0] == current[0] == "file":
                left, right = (before / name).read_bytes(), (after / name).read_bytes()
                try:
                    if b"\0" in left + right:
                        raise UnicodeError
                    lines.extend(difflib.unified_diff(
                        left.decode().splitlines(keepends=True),
                        right.decode().splitlines(keepends=True),
                        fromfile=f"before/{name}", tofile=f"after/{name}"))
                except UnicodeError:
                    lines.append("Binary contents differ; see snapshots.\n")
        report = "".join(lines)
        (diagnostics / "diff.txt").write_text(report)
        print(f"error: checkout changed during smoke: {repo}; possible test leak or concurrent edit (left untouched)", file=sys.stderr)
        print(report, end="", file=sys.stderr)
        print(f"smoke diagnostics: {diagnostics}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    command, repo, before, *args = sys.argv[1:]
    if command == "snapshot":
        snapshot(Path(repo), Path(before), tracked_paths(Path(repo)))
    elif command == "check":
        sys.exit(check(Path(repo), Path(before), Path(args[0])))
    else:
        raise SystemExit(f"unknown checkout guard command: {command}")

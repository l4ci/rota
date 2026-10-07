#!/usr/bin/env python3
"""Doc drift guard (#392): every docs/design/contract/*.md is stamped with the
commit it was last verified against and the repo paths it describes.

    ---
    verified-sha: <commit sha>
    refs:
      - internal/gate
      - internal/cli/gate.go
    ---

A doc fails when its stamp is missing or malformed, the sha is unknown, a ref
does not exist, or a ref changed between the sha and HEAD (`git diff --quiet
<sha> HEAD -- <ref>`). The failure names the doc and the changed paths. To clear
it, re-read the doc against the changed paths, fix what drifted, then set
`verified-sha:` to the current `git rev-parse HEAD` (docs/contributing/docs-stamps.md).

Usage: check-doc-stamps.py [--root <repo>]   (default: this checkout)
Exit:  0 all stamped and current, 1 findings (one line each on stdout)
"""
import argparse
import re
import subprocess
import sys
from pathlib import Path

SHA_RE = re.compile(r"^[0-9a-f]{7,40}$")


def parse_stamp(text):
    """Return (sha, refs, error) from a doc's leading frontmatter block."""
    lines = text.split("\n")
    if not lines or lines[0].strip() != "---":
        return None, [], "no frontmatter (expected a leading --- block)"
    try:
        end = lines.index("---", 1)
    except ValueError:
        return None, [], "frontmatter is not closed with ---"
    sha, refs, in_refs = None, [], False
    for raw in lines[1:end]:
        if not raw.strip():
            continue
        item = re.match(r"^\s+-\s+(\S.*?)\s*$", raw)
        if in_refs and item:
            refs.append(item.group(1))
            continue
        in_refs = False
        key = re.match(r"^([A-Za-z-]+):\s*(.*?)\s*$", raw)
        if not key:
            return None, [], f"unparseable frontmatter line: {raw!r}"
        if key.group(1) == "verified-sha":
            sha = key.group(2)
        elif key.group(1) == "refs":
            if key.group(2):
                return None, [], "refs: must be a block list (one `  - path` per line)"
            in_refs = True
    if sha is None:
        return None, refs, "missing verified-sha:"
    if not SHA_RE.match(sha):
        return None, refs, f"malformed verified-sha: {sha!r} (want 7 to 40 hex chars)"
    if not refs:
        return None, refs, "missing refs: (a list of repo paths)"
    return sha, refs, None


def git(root, *args):
    return subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True)


def check_doc(root, doc):
    name = doc.relative_to(root).as_posix()
    sha, refs, err = parse_stamp(doc.read_text())
    if err:
        return [f"{name}: {err}"]
    if git(root, "cat-file", "-e", f"{sha}^{{commit}}").returncode != 0:
        return [f"{name}: verified-sha {sha} is not a known commit (shallow clone? fetch full history)"]
    findings = []
    for ref in refs:
        if ref.startswith("/") or ".." in Path(ref).parts:
            findings.append(f"{name}: ref {ref!r} is not a repo-relative path")
        elif not (root / ref).exists() and git(root, "cat-file", "-e", f"{sha}:{ref}").returncode != 0:
            findings.append(f"{name}: ref {ref} does not exist")
    if findings:
        return findings
    # A ref that existed at the sha but is gone now is drift too (the diff sees the delete).
    changed = [r for r in refs if git(root, "diff", "--quiet", sha, "HEAD", "--", r).returncode != 0]
    if changed:
        return [f"{name}: refs changed since verified-sha {sha}: {', '.join(changed)}"]
    return []


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=str(Path(__file__).resolve().parent.parent))
    root = Path(ap.parse_args().root).resolve()
    docs = sorted((root / "docs/design/contract").glob("*.md"))
    if not docs:
        print("docs/design/contract: no contract docs found")
        return 1
    findings = [f for d in docs for f in check_doc(root, d)]
    for f in findings:
        print(f)
    if not findings:
        print(f"OK {len(docs)} contract docs stamped and current")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())

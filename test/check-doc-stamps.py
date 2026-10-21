#!/usr/bin/env python3
"""Doc drift guard (#392): every docs/contributing/contract/*.md names the repo paths it
describes in its frontmatter, and the commit it was last verified against in one
sidecar, docs/contributing/contract/STAMPS (#1294).

    ---                                   # the doc
    refs:
      - internal/gate
      - internal/cli/gate.go
    ---

    gates.md c535c4cf9bd5b3b92d47e1d1df0c0014b4bfe750     # STAMPS: one `<doc> <sha>` line per doc

The sha lives outside the docs so two PRs editing one doc never collide on a stamp
line: PRs leave STAMPS alone and main re-stamps it (--restamp) after a merge.

A doc has an error when its stamp is missing or malformed, the sha is unknown,
or a ref does not exist: a mistake in the stamp itself, always exit 1. A doc has
drift when a ref changed between the sha and HEAD (`git diff --quiet <sha> HEAD
-- <ref>`). Drift is report-only by default (printed as `WARN`, exit 0) so
unrelated PRs are not blocked; `--strict` (or ROTA_DOC_STAMPS=strict) fails on it.
Either way the line names the doc and the changed paths. To clear drift, re-read
the doc against the changed paths, fix what drifted, then run `--restamp <doc>` on
main (docs/contributing/rounds.md, Contract doc stamps).

Usage: check-doc-stamps.py [--root <repo>] [--strict]   (default root: this checkout)
       check-doc-stamps.py [--root <repo>] --restamp <doc.md>...   (set those stamps to HEAD)
Exit:  0 no errors (and no drift when strict), 1 otherwise (one line each on stdout)
"""
import os
import argparse
import re
import subprocess
import sys
from pathlib import Path

SHA_RE = re.compile(r"^[0-9a-f]{7,40}$")
STAMPS = "docs/contributing/contract/STAMPS"


def parse_refs(text):
    """Return (refs, error) from a doc's leading frontmatter block."""
    lines = text.split("\n")
    if not lines or lines[0].strip() != "---":
        return [], "no frontmatter (expected a leading --- block)"
    try:
        end = lines.index("---", 1)
    except ValueError:
        return [], "frontmatter is not closed with ---"
    refs, in_refs = [], False
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
            return [], f"unparseable frontmatter line: {raw!r}"
        if key.group(1) == "verified-sha":
            return [], f"verified-sha: belongs in {STAMPS}, not in the doc"
        elif key.group(1) == "refs":
            if key.group(2):
                return [], "refs: must be a block list (one `  - path` per line)"
            in_refs = True
    if not refs:
        return [], "missing refs: (a list of repo paths)"
    return refs, None


def read_stamps(root):
    """Return ({doc name: sha}, errors) from the STAMPS sidecar."""
    path = root / STAMPS
    if not path.exists():
        return {}, []
    stamps, errors = {}, []
    for n, raw in enumerate(path.read_text().split("\n"), 1):
        if not raw.strip() or raw.startswith("#"):
            continue
        parts = raw.split()
        if len(parts) != 2 or not parts[0].endswith(".md"):
            errors.append(f"{STAMPS}:{n}: want `<doc>.md <sha>`, got {raw!r}")
        elif parts[0] in stamps:
            errors.append(f"{STAMPS}:{n}: duplicate stamp for {parts[0]}")
        else:
            stamps[parts[0]] = parts[1]
    return stamps, errors


def git(root, *args):
    return subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True)


def check_doc(root, doc, stamps):
    """Return (errors, drift): lists of finding lines."""
    name = doc.relative_to(root).as_posix()
    refs, err = parse_refs(doc.read_text())
    if err:
        return [f"{name}: {err}"], []
    sha = stamps.get(doc.name)
    if sha is None:
        return [f"{name}: missing stamp (no `{doc.name} <sha>` line in {STAMPS})"], []
    if not SHA_RE.match(sha):
        return [f"{name}: malformed verified-sha: {sha!r} (want 7 to 40 hex chars)"], []
    if git(root, "cat-file", "-e", f"{sha}^{{commit}}").returncode != 0:
        return [f"{name}: verified-sha {sha} is not a known commit (shallow clone? fetch full history)"], []
    findings = []
    for ref in refs:
        if ref.startswith("/") or ".." in Path(ref).parts:
            findings.append(f"{name}: ref {ref!r} is not a repo-relative path")
        elif not (root / ref).exists() and git(root, "cat-file", "-e", f"{sha}:{ref}").returncode != 0:
            findings.append(f"{name}: ref {ref} does not exist")
    if findings:
        return findings, []
    # A ref that existed at the sha but is gone now is drift too (the diff sees the delete).
    changed = [r for r in refs if git(root, "diff", "--quiet", sha, "HEAD", "--", r).returncode != 0]
    if changed:
        return [], [f"{name}: refs changed since verified-sha {sha}: {', '.join(changed)}"]
    return [], []


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=str(Path(__file__).resolve().parent.parent))
    ap.add_argument("--strict", action="store_true",
                    default=os.environ.get("ROTA_DOC_STAMPS") == "strict",
                    help="fail on drifted refs too (also ROTA_DOC_STAMPS=strict)")
    ap.add_argument("--restamp", nargs="+", metavar="DOC",
                    help="set these docs' stamps to HEAD (main only, after re-reading each against its refs)")
    args = ap.parse_args()
    root = Path(args.root).resolve()
    docs = sorted((root / "docs/contributing/contract").glob("*.md"))
    if not docs:
        print("docs/contributing/contract: no contract docs found")
        return 1
    stamps, errors = read_stamps(root)
    if args.restamp:
        names = {d.name for d in docs}
        unknown = [d for d in map(os.path.basename, args.restamp) if d not in names]
        if errors or unknown:
            print("\n".join(errors + [f"{d}: not a contract doc" for d in unknown]))
            return 1
        head = git(root, "rev-parse", "HEAD").stdout.strip()
        stamps.update({os.path.basename(d): head for d in args.restamp})
        (root / STAMPS).write_text("".join(f"{n} {stamps[n]}\n" for n in sorted(stamps)))
        print(f"OK restamped {len(args.restamp)} doc(s) at {head}")
        return 0
    errors += [f"{STAMPS}: stamp for {n} has no doc" for n in sorted(set(stamps) - {d.name for d in docs})]
    results = [check_doc(root, d, stamps) for d in docs]
    errors += [f for e, _ in results for f in e]
    drift = [f for _, d in results for f in d]
    for f in errors:
        print(f)
    for f in drift:
        print(f if args.strict else f"WARN {f}")
    if not errors and not drift:
        print(f"OK {len(docs)} contract docs stamped and current")
    elif not errors and not args.strict:
        print(f"WARN {len(drift)} contract doc(s) drifted from their refs (ROTA_DOC_STAMPS=strict fails on this)")
    return 1 if errors or (drift and args.strict) else 0


if __name__ == "__main__":
    sys.exit(main())

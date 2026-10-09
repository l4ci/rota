#!/usr/bin/env python3
"""Lint smoke sections for bare verb captures (#579).

Sections run under `set -euo pipefail`. `VAR=$(rota ...)` (or `VAR="$(...)"`)
aborts the section silently when the verb exits non-zero (#461). Every capture
of a rota call, on one line or several, must be followed by `|| <handler>` on
the line where the substitution closes.

Usage: lint-sections.py [--fix] DIR_OR_FILE...
  default  print `file:line: text` per bare capture, exit 1 if any
  --fix    append ` || vfail` to each (test/lib.sh defines vfail)
"""
import re, sys, glob, os

START = re.compile(r'^(\s*)([A-Za-z_][A-Za-z0-9_]*)=("?)\$\(')
VERB = re.compile(r'ROTA_BIN|\bhvj\b|\brota\s')


def close_of(text, i):
    """Index just past the `)` that closes the `$(` whose `(` is at text[i]."""
    depth, q, n = 0, None, len(text)
    while i < n:
        c = text[i]
        if q == "'":
            if c == "'":
                q = None
        elif c == "\\":
            i += 1
        elif c == "'" and q is None:
            q = "'"
        elif c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return i + 1
        i += 1
    return -1


def scan(path):
    src = open(path).read()
    lines = src.split("\n")
    offs, o = [], 0
    for l in lines:
        offs.append(o)
        o += len(l) + 1
    found = []
    # A section-local helper (`gt_gate() { ... || rc=$?; echo "$rc"; }`) owns its
    # exit code: capturing its output is not a bare verb call.
    funcs = set(re.findall(r"^(?:function\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\)", src, re.M))
    for ln, l in enumerate(lines):
        m = START.match(l)
        if not m or l.lstrip().startswith("#"):
            continue
        prev = lines[ln - 1].rstrip() if ln else ""
        if prev.endswith(("||", "&&", "|", "\\")) or re.match(r"\s*(if|while|until|elif)\b", l):
            continue
        paren = offs[ln] + m.end() - 1
        end = close_of(src, paren)
        if end < 0 or not VERB.search(src[paren:end]):
            continue
        first = re.match(r"\(\s*\"?([A-Za-z_][A-Za-z0-9_]*)", src[paren:end])
        if first and first.group(1) in funcs:
            continue
        eol = src.find("\n", end)
        eol = len(src) if eol < 0 else eol
        tail = src[end:eol]
        if tail.rstrip().endswith("\\"):  # the handler may open the next line
            nxt = src[eol + 1: src.find("\n", eol + 1)].lstrip() if eol < len(src) else ""
            tail += " " + nxt
        # `&&` after the capture is the expected-failure idiom: the verb is meant
        # to fail, and an `&&` list never trips errexit. A `|| true` inside the
        # substitution already swallows the exit code on purpose.
        if "||" in tail or "&&" in tail or re.search(r"\|\|\s*(true|:)\s*\)", src[paren:end] ):
            continue
        # `)" ` closes a quoted capture; the tail may carry a redirect.
        endline = src.count("\n", 0, end)
        found.append((endline, eol))
    return src, found


def main(argv):
    fix = "--fix" in argv
    paths = []
    for a in argv:
        if a == "--fix":
            continue
        paths += sorted(glob.glob(os.path.join(a, "*.sh"))) if os.path.isdir(a) else [a]
    bad = 0
    for p in paths:
        src, found = scan(p)
        if not found:
            continue
        if fix:
            for _, eol in sorted(found, key=lambda t: -t[1]):
                src = src[:eol].rstrip() + " || vfail" + src[eol:]
            open(p, "w").write(src)
        else:
            lines = src.split("\n")
            for endline, _ in found:
                print(f"{p}:{endline + 1}: {lines[endline].strip()}")
        bad += len(found)
    return 0 if (fix or not bad) else 1


sys.exit(main(sys.argv[1:]))

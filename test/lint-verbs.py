# Verb-existence lint (#63, moved out of smoke by #79): every `rota <group> <verb>` a doc names must exist.
# Usage: python3 test/lint-verbs.py <rota-binary> <doc.md>...
# A verb resolves when `rota <words> --help` descends the tree: each group's help
# lists its Commands and the next word must be one of them; a leaf ends the walk
# (later words are positional arguments). Extracted words are the run of
# lowercase [a-z-] tokens after `rota` in an inline code span or a fenced line;
# flags, placeholders, quotes and `rota-*` skill names end or never match.
import re, subprocess, sys
rota = sys.argv[1]
docs = sys.argv[2:]

def commands(path):
    out = subprocess.run([rota] + path + ["--help"], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    names, on = [], False
    for line in out.stdout.splitlines():
        if line.startswith("Commands:"):
            on = True
        elif on and line.strip():
            names.append(line.split()[0])
        elif on:
            break
    return names

found = {}
for doc in docs:
    fence = False
    for n, line in enumerate(open(doc), 1):
        if line.lstrip().startswith("```"):
            fence = not fence
            continue
        for span in ([line.strip()] if fence else re.findall(r"`([^`]+)`", line)):
            toks = span.split()
            if not toks or toks[0] != "rota":
                continue
            words = []
            for t in toks[1:]:
                if not re.fullmatch(r"[a-z][a-z-]*", t):
                    break
                words.append(t)
            if words:
                found.setdefault(tuple(words), "%s:%d" % (doc, n))

bad, ok = [], 0
for words, where in sorted(found.items()):
    path = []
    verdict = "ok"
    for w in words:
        cmds = commands(path)
        if cmds is None:
            verdict = "no help for rota %s" % " ".join(path)
            break
        if not cmds:          # a leaf: the rest are arguments
            break
        if w not in cmds:
            verdict = "rota %s has no command %r" % (" ".join(path) or "<root>", w)
            break
        path.append(w)
    if verdict == "ok" and not path:
        verdict = "names no verb"
    verb = " ".join(path) if verdict == "ok" else " ".join(words[:2])
    if verdict != "ok":
        bad.append("%s: rota %s: %s" % (where, " ".join(words), verdict))
    else:
        ok += 1
for b in bad:
    print("MISSING " + b)
print("RESOLVED %d" % ok)
sys.exit(1 if bad else 0)

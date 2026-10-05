#!/usr/bin/env python3
"""Fake gh and glab for the worker gate: one PR (gh) or MR (glab) over a real bare origin.

Same script as smoke section 68 builds inline. Env: FORGE_TOOL (gh|glab), FORGE_DB (json
state file), FORGE_LOG (argv log), FORGE_MODE (ok, race, fail, noop, elsewhere, ff, squash).
Merging clones FORGE_DB's origin into a temp dir and pushes there, so it needs git only.
Safe by construction: it never touches herdr, tmux or a network.
"""
import json, os, subprocess, sys, tempfile

tool, args = os.environ["FORGE_TOOL"], sys.argv[1:]
dbp, mode = os.environ["FORGE_DB"], os.environ.get("FORGE_MODE", "ok")
db = json.load(open(dbp))
with open(os.environ["FORGE_LOG"], "a") as f:
    f.write(tool + " " + " ".join(args) + "\n")

def save(): json.dump(db, open(dbp, "w"))

def merge():
    # A real forge refuses a merge whose pin is missing or is not the PR's head.
    flag = "--match-head-commit" if tool == "gh" else "--sha"
    pin = args[args.index(flag) + 1] if flag in args else None
    if mode == "race":
        db["sha"] = "f" * 40  # someone pushed after the gate's check
    if pin != db["sha"]:
        print("simulated: head commit %s does not match the pin %s" % (db["sha"], pin), file=sys.stderr); sys.exit(1)
    if mode == "fail":
        print("simulated: merge blocked by branch protection", file=sys.stderr); sys.exit(1)
    if mode == "noop":
        return
    target = "stack" if mode == "elsewhere" else db["base"]
    with tempfile.TemporaryDirectory() as t:
        def g(*a): return subprocess.run(["git", "-c", "user.name=f", "-c", "user.email=f@f", *a],
                                         cwd=t, check=True, capture_output=True, text=True).stdout.strip()
        subprocess.run(["git", "clone", "-q", db["origin"], t], check=True)
        if mode == "elsewhere": g("checkout", "-q", "-b", "stack", "origin/" + db["base"])
        if mode == "ff":  # fast-forward method: no merge commit on the MR
            g("merge", "--ff-only", "-q", "origin/" + db["head"])
        else:
            g("merge", "--no-ff", "-q", "-m", "merge pr", "origin/" + db["head"])
            db["merge"] = g("rev-parse", "HEAD")
        g("push", "-q", "origin", target)
    db["state"] = "MERGED"; save()

if tool == "gh" and args[:2] == ["pr", "view"]:
    print(json.dumps({"headRefName": db["head"], "headRefOid": db["sha"], "baseRefName": db["base"],
                      "state": db["state"], "body": db["body"],
                      "mergeCommit": {"oid": db["merge"]} if db["merge"] else None}))
elif tool == "gh" and args[:2] == ["pr", "merge"]:
    merge()
elif tool == "glab" and args[:1] == ["api"]:
    squash = mode == "squash"
    print(json.dumps({"source_branch": db["head"], "sha": db["sha"], "target_branch": db["base"],
                      "state": {"OPEN": "opened", "MERGED": "merged"}.get(db["state"], "closed"),
                      "description": db["body"],
                      "merge_commit_sha": None if squash or not db["merge"] else db["merge"],
                      "squash_commit_sha": db["merge"] if squash and db["merge"] else None}))
elif tool == "glab" and args[:2] == ["mr", "merge"]:
    merge()
else:
    print("fake forge: unsupported: %s" % args, file=sys.stderr); sys.exit(2)

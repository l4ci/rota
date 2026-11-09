# Seed the shared fake gh (test/fakes/gh, backed by fake_tracker.py) with the one PR a
# gate section drives, so no section carries its own gh stub.
#
#   forge_state <db> <sha> <body> <labels-json> <closing-csv> <issue-number>...
#
# PR 7 is open on branch ben/5-thing (or $FORGE_STATE_HEAD) against main at <sha>. <body> is the PR body (a
# literal \n is a newline); the gate's `## Assumptions` and `## Rulings` sections are
# appended. <labels-json> is the inside of a JSON list, e.g. '"risk:high"', put on every
# issue. <closing-csv> lists the issues the forge says merging the PR closes. Each
# <issue-number> becomes an open issue. Run the binary with
# PATH="$TESTDIR/fakes:..." FAKE_TRACKER_DB=<db>; the fake rejects any call it does not know.
forge_state() {
  python3 - "$@" <<'PYEOF'
import json, os, sys
db, sha, body, labels, closing = sys.argv[1:6]
issues = [int(n) for n in sys.argv[6:]]
body = body.replace("\\n", "\n") + "\n## Assumptions\nNone\n\n## Rulings\nNone\n"
json.dump({
    "next_issue": 100, "next_milestone": 1, "next_comment": 1, "next_mr": 1, "labels": [], "milestones": [],
    "issues": [{"number": n, "title": "t", "body": "", "labels": json.loads("[%s]" % labels), "milestone": None,
                "state": "open", "state_reason": None, "closed_at": None, "assignees": [], "comments": []}
               for n in issues],
    "prs": [{"number": 7, "title": "t", "body": body, "head": os.environ.get("FORGE_STATE_HEAD", "ben/5-thing"), "base": "main",
             "state": "open", "sha": sha}],
    "closing_refs": {"7": [int(n) for n in closing.split(",") if n]},
}, open(db, "w"))
PYEOF
}

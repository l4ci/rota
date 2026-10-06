#!/usr/bin/env python3
"""Stateful fake `gh` (2.45) and `glab` (1.120) for offline tests.

Usage (via the front-ends): fake_tracker.py <gh|glab> ARGV...
Env: FAKE_TRACKER_DB   JSON store path (required unless FAKE_TRACKER_DB_DIR; created on first write)
     FAKE_TRACKER_DB_DIR  per-repo stores: <dir>/<basename of the cwd's git toplevel>.json (wins over FAKE_TRACKER_DB)
     FAKE_TRACKER_LOG  if set, each call's argv (space-joined) is appended
     FAKE_TRACKER_FAIL if set, any call whose argv contains it fails (exit 1)
     FAKE_TRACKER_FAIL_MSG extra stderr text on that failure (e.g. "secondary rate limit" makes rota exit 4)
Only the subset rota uses is implemented; anything else exits 2.
Ids are realistic where rota must not mix them up: gh `issue view --json comments` gives
GraphQL node ids (the REST id is in the comment url), and glab's global `id` differs from
the project-scoped `iid` for milestones, which the API edits by `id` (offset by GL_MILESTONE_ID).
"""
import json
import os
import re
import sys

# hashlib, subprocess and datetime load on first use: a fake runs thousands of times per
# smoke run, and each import costs 5 to 18 ms of interpreter start (#84).
def _run(*a, **k):
    import subprocess
    return subprocess.run(*a, **k)


def _sha1(b):
    import hashlib
    return hashlib.sha1(b)


USER = "fake-user"
GL_MILESTONE_ID = 1000  # glab milestone `id` = iid + this


class Fail(Exception):
    def __init__(self, msg, code=1):
        super().__init__(msg)
        self.code = code


# ---------------------------------------------------------------- store
def db_path():
    d = os.environ.get("FAKE_TRACKER_DB_DIR")
    if d:
        r = _run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True)
        if r.returncode != 0:
            raise Fail("FAKE_TRACKER_DB_DIR needs a git repo cwd")
        return os.path.join(d, os.path.basename(r.stdout.strip()) + ".json")
    p = os.environ.get("FAKE_TRACKER_DB")
    if not p:
        raise Fail("FAKE_TRACKER_DB is required")
    return p


def load():
    try:
        with open(db_path()) as f:
            return json.load(f)
    except FileNotFoundError:
        return {"next_issue": 1, "next_milestone": 1, "next_comment": 1,
                "issues": [], "labels": [], "milestones": [], "prs": [],
                "next_mr": 1}


def save(db):
    tmp = db_path() + ".tmp"
    with open(tmp, "w") as f:
        json.dump(db, f, indent=1)
    os.replace(tmp, db_path())


def now():
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def find_issue(db, n):
    for i in db["issues"]:
        if i["number"] == int(n):
            return i
    raise Fail("issue #%s not found" % n)


def find_milestone(db, title):
    for m in db["milestones"]:
        if m["title"] == title:
            return m
    return None


def new_issue(db, title, body, labels, milestone):
    issue = {"number": db["next_issue"], "title": title, "body": body or "",
             "labels": list(labels), "milestone": milestone, "state": "open",
             "state_reason": None, "closed_at": None, "assignees": [],
             "comments": []}
    db["next_issue"] += 1
    db["issues"].append(issue)
    return issue


def add_comment(db, issue, body):
    c = {"id": db["next_comment"], "body": body, "author": USER}
    db["next_comment"] += 1
    issue["comments"].append(c)
    return c


def thread(db, n):
    """The issue-comments API's target: an issue, or (as on GitHub, where a PR is
    an issue) a gh PR, whose thread comments live in their own list so the plain
    `gh pr comment` strings stay apart."""
    for i in db["issues"]:
        if i["number"] == int(n):
            return i
    for p in db.get("prs", []):
        if p["number"] == int(n):
            return {"number": p["number"], "comments": p.setdefault("thread_comments", [])}
    raise Fail("issue #%s not found" % n)


def threads(db):
    """Every comment list the issue-comments API reaches, with its number."""
    out = [(i["number"], i["comments"]) for i in db["issues"]]
    out += [(p["number"], p["thread_comments"]) for p in db.get("prs", []) if "thread_comments" in p]
    return out


def new_milestone(db, title, description, state):
    if find_milestone(db, title):
        raise Fail("milestone %r already exists" % title)
    m = {"number": db["next_milestone"], "title": title,
         "description": description or "", "state": state}
    db["next_milestone"] += 1
    db["milestones"].append(m)
    return m


def close_issue(issue, reason=None):
    issue["state"] = "closed"
    issue["state_reason"] = reason
    issue["closed_at"] = now()


# ---------------------------------------------------------------- args
def parse(argv, value_flags, bool_flags=()):
    """value_flags: {flag: dest}; repeated flags collect into lists.
    Returns (opts{dest: [values]}, bools{set of dest-ish flags}, positionals)."""
    opts, bools, pos = {}, set(), []
    i = 0
    while i < len(argv):
        a = argv[i]
        if a.startswith("-") and a != "-":
            flag, eq, val = a.partition("=") if a.startswith("--") else (a, "", "")
            if flag in value_flags:
                if not eq:
                    i += 1
                    if i >= len(argv):
                        raise Fail("flag needs an argument: %s" % flag)
                    val = argv[i]
                opts.setdefault(value_flags[flag], []).append(val)
            elif flag in bool_flags:
                bools.add(flag)
            else:
                raise Fail("unknown flag: %s" % a, 2)
        else:
            pos.append(a)
        i += 1
    return opts, bools, pos


def one(opts, key, default=None):
    return opts[key][-1] if key in opts else default


def text_arg(opts, text_key, file_key):
    """--body / --body-file ('-' reads stdin)."""
    if file_key in opts:
        f = opts[file_key][-1]
        return sys.stdin.read() if f == "-" else open(f).read()
    return one(opts, text_key)


def emit(obj):
    print(json.dumps(obj))


def pick(full, fields):
    return {f: full[f] for f in fields if f in full}


def split_labels(vals):
    out = []
    for v in vals:
        out += [x for x in v.split(",") if x]
    return out


# ---------------------------------------------------------------- pull / merge requests
def new_pr(db, tool, title, body, head, base):
    """gh PRs share the issue counter (as on GitHub); glab MRs have their own."""
    key = "next_issue" if tool == "gh" else "next_mr"
    n = db.get(key, 1)
    db[key] = n + 1
    pr = {"number": n, "title": title, "body": body or "", "head": head, "base": base,
          "state": "open", "sha": _sha1(("fake-head-%s" % head).encode()).hexdigest()}
    db.setdefault("prs", []).append(pr)
    return pr


def gh_pr(p):
    return {"number": p["number"], "title": p["title"], "body": p["body"],
            "headRefName": p["head"], "baseRefName": p["base"], "state": p["state"].upper(),
            "headRefOid": p.get("sha", ""),
            "mergeCommit": {"oid": p["merge_sha"]} if p.get("merge_sha") else None,
            "url": "https://github.com/fake/repo/pull/%d" % p["number"]}


def gl_mr(p):
    out = {"iid": p["number"], "title": p["title"], "description": p["body"],
           "source_branch": p["head"], "target_branch": p["base"], "sha": p.get("sha", ""),
           "state": "opened" if p["state"] == "open" else p["state"],
           "web_url": "https://gitlab.com/fake/repo/-/merge_requests/%d" % p["number"]}
    if p.get("merge_sha"):
        out["merge_commit_sha"] = p["merge_sha"]
    return out


CLOSING_RE = re.compile(
    r"(?<![\w])(?:close[sd]?|fix(?:es|ed)?|resolve[sd]?|implement(?:s|ed)?)\b:?\s+#(\d+)(?!\d)",
    re.I)


def find_pr(prs, want, mr=False):
    hit = [p for p in prs if str(p["number"]) == want.lstrip("#") or p["head"] == want]
    if not hit:
        raise Fail("404 Not Found" if mr else "no pull requests found for %s" % want)
    return hit[-1]


def current_branch():
    """Head of a PR opened without --head: the branch checked out in the cwd repo (as gh does)."""
    r = _run(["git", "rev-parse", "--abbrev-ref", "HEAD"], capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 and r.stdout.strip() else "HEAD"


def checkout_pr(p):
    """Create/switch a local branch named after the PR head in the cwd repo."""
    head = p["head"]
    have = _run(["git", "rev-parse", "--verify", "-q", "refs/heads/" + head],
                          capture_output=True).returncode == 0
    cmd = ["git", "checkout", "-q", head] if have else ["git", "checkout", "-q", "-B", head]
    r = _run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise Fail("checkout failed: " + r.stderr.strip())


def check_pin(p, want):
    """The forges refuse a pinned merge when the head has moved."""
    if want and want != p.get("sha"):
        raise Fail("Head branch was modified. Review the latest changes and try again.")


def merge_pr(db, p):
    """Mark merged with a deterministic fake sha; like the real hosts, linked
    issues close automatically only when the base is the default branch."""
    if p["state"] != "open":
        raise Fail("pull request is not open")
    p["state"] = "merged"
    p["merge_sha"] = _sha1(("fake-merge-%d" % p["number"]).encode()).hexdigest()
    if p["base"] == "main":
        for m in CLOSING_RE.finditer(p["body"]):
            for i in db["issues"]:
                if i["number"] == int(m.group(1)) and i["state"] == "open":
                    close_issue(i, "completed")
    save(db)


GH_PR_FLAGS = {"--title": "title", "-t": "title", "--body": "body", "-b": "body",
               "--body-file": "body_file", "-F": "body_file", "--base": "base", "-B": "base",
               "--head": "head", "-H": "head", "--json": "json", "--state": "state", "-s": "state",
               "--limit": "limit", "-L": "limit", "--match-head-commit": "match_head"}
GH_PR_BOOLS = ("--merge", "--squash", "--rebase", "--delete-branch", "-d", "-m")


def gh_pr_cmd(db, args):
    verb = args[0]
    o, b, pos = parse(args[1:], GH_PR_FLAGS, GH_PR_BOOLS)
    fields = split_labels(o.get("json", []))
    prs = [p for p in db.get("prs", []) if not p.get("mr")]
    if verb == "create":
        head = one(o, "head") or current_branch()
        p = new_pr(db, "gh", one(o, "title", ""), text_arg(o, "body", "body_file"), head,
                   one(o, "base", "main"))
        save(db)
        print(gh_pr(p)["url"])
    elif verb == "list":
        state = one(o, "state", "open")
        rows = [p for p in prs if state == "all" or p["state"] == state]
        rows.sort(key=lambda p: -p["number"])
        emit([pick(gh_pr(p), fields) for p in rows[:int(one(o, "limit", 30))]])
    elif verb == "view":
        want = pos[0]
        hit = [p for p in prs if str(p["number"]) == want.lstrip("#") or p["head"] == want]
        if not hit:
            raise Fail("no pull requests found for %s" % want)
        emit(pick(gh_pr(hit[-1]), fields)) if fields else print("title:\t%s" % hit[-1]["title"])
    elif verb == "checkout":
        checkout_pr(find_pr(prs, pos[0]))
    elif verb == "merge":
        p = find_pr(prs, pos[0])
        check_pin(p, one(o, "match_head"))
        merge_pr(db, p)
    elif verb == "comment":
        find_pr(prs, pos[0]).setdefault("comments", []).append(text_arg(o, "body", "body_file") or "")
        save(db)
    else:
        raise Fail("unsupported", 2)


GL_MR_FLAGS = {"--title": "title", "-t": "title", "--description": "desc", "-d": "desc",
               "--source-branch": "head", "-s": "head", "--target-branch": "base", "-b": "base",
               "--output": "output", "-O": "output", "--per-page": "per_page", "-P": "per_page",
               "--state": "state", "--message": "message", "-m": "message",
               "--auto-merge": "auto_merge", "--sha": "sha"}


def gl_mr_cmd(db, args):
    verb = args[0]
    o, b, pos = parse(args[1:], GL_MR_FLAGS, ("--yes", "-y", "--all", "-A", "--closed", "-c", "--merged", "-M", "--remove-source-branch"))
    prs = [p for p in db.get("prs", []) if p.get("mr")]
    if verb == "create":
        p = new_pr(db, "glab", one(o, "title", ""), one(o, "desc"), one(o, "head", "HEAD"),
                   one(o, "base", "main"))
        p["mr"] = True
        save(db)
        print(gl_mr(p)["web_url"])
    elif verb == "list":
        rows = [p for p in prs if ("--all" in b or "-A" in b) or p["state"] == "open"]
        rows.sort(key=lambda p: -p["number"])
        emit([gl_mr(p) for p in rows[:min(int(one(o, "per_page", 30)), 100)]])
    elif verb == "view":
        want = pos[0]
        hit = [p for p in prs if str(p["number"]) == want.lstrip("#") or p["head"] == want]
        if not hit:
            raise Fail("404 Not Found")
        emit(gl_mr(hit[-1]))
    elif verb == "checkout":
        checkout_pr(find_pr(prs, pos[0], True))
    elif verb == "merge":
        p = find_pr(prs, pos[0], True)
        check_pin(p, one(o, "sha"))
        merge_pr(db, p)
    elif verb == "note":
        find_pr(prs, pos[0], True).setdefault("comments", []).append(one(o, "message", ""))
        save(db)
    else:
        raise Fail("unsupported", 2)


# ---------------------------------------------------------------- gh
def gh_issue(i, base_url="https://github.com/fake/repo"):
    return {
        "number": i["number"], "title": i["title"], "body": i["body"],
        "labels": [{"name": n} for n in i["labels"]],
        "milestone": ({"title": i["milestone"][0], "number": i["milestone"][1]}
                      if i["milestone"] else None),
        "state": i["state"].upper(),
        "stateReason": (i["state_reason"] or "").upper().replace(" ", "_") or None,
        "url": "%s/issues/%d" % (base_url, i["number"]),
        "closedAt": i["closed_at"],
        "assignees": [{"login": a} for a in i["assignees"]],
        "comments": [{"id": "IC_fake%d" % c["id"], "body": c["body"],
                      "url": "%s/issues/%d#issuecomment-%d" % (base_url, i["number"], c["id"]),
                      "author": {"login": c["author"]}} for c in i["comments"]],
    }


def gh_milestone_ref(db, title):
    m = find_milestone(db, title)
    if not m:
        raise Fail("could not add to milestone '%s': '%s' not found" % (title, title))
    return [m["title"], m["number"]]


def gh_check_labels(db, labels):
    for n in labels:
        if n not in db["labels"]:
            raise Fail("could not add label: '%s' not found" % n)


GH_ISSUE_FLAGS = {
    "--title": "title", "-t": "title", "--body": "body", "-b": "body",
    "--body-file": "body_file", "-F": "body_file", "--label": "label", "-l": "label",
    "--milestone": "milestone", "-m": "milestone", "--state": "state", "-s": "state",
    "--limit": "limit", "-L": "limit", "--json": "json", "--add-label": "add_label",
    "--remove-label": "remove_label", "--add-assignee": "add_assignee",
    "--remove-assignee": "remove_assignee", "--reason": "reason", "-r": "reason",
    "--comment": "comment", "-c": "comment", "-R": "repo", "--repo": "repo",
    "-q": "jq", "--jq": "jq",
}


def gh_issue_cmd(db, args):
    verb, rest = args[0], args[1:]
    o, b, pos = parse(rest, GH_ISSUE_FLAGS, ("--remove-milestone",))
    fields = split_labels(o.get("json", []))
    if verb == "create":
        labels = split_labels(o.get("label", []))
        gh_check_labels(db, labels)
        ms = gh_milestone_ref(db, one(o, "milestone")) if "milestone" in o else None
        body = text_arg(o, "body", "body_file")
        i = new_issue(db, one(o, "title", ""), body, labels, ms)
        if "repo" in o:  # -R <owner/repo>: the issue lands there; the store records the target
            i["repo"] = one(o, "repo")
        save(db)
        print(gh_issue(i, "https://github.com/%s" % i["repo"] if "repo" in i else
                       "https://github.com/fake/repo")["url"])
    elif verb == "list":
        state = one(o, "state", "open")
        want = split_labels(o.get("label", []))
        rows = [i for i in db["issues"]
                if (state == "all" or i["state"] == state)
                and all(w in i["labels"] for w in want)
                and ("milestone" not in o or (i["milestone"] and i["milestone"][0] == one(o, "milestone")))]
        rows.sort(key=lambda i: -i["number"])
        rows = rows[:int(one(o, "limit", 30))]
        emit([pick(gh_issue(i), fields) for i in rows])
    elif verb == "view":
        i = find_issue(db, pos[0])
        jq = one(o, "jq")
        if jq and re.fullmatch(r"\.\w+", jq) and fields:  # only the `.field` filter
            v = pick(gh_issue(i), fields).get(jq[1:])
            print(v if isinstance(v, str) else json.dumps(v))
        else:
            emit(pick(gh_issue(i), fields)) if fields else print("title:\t%s" % i["title"])
    elif verb == "edit":
        i = find_issue(db, pos[0])
        if "title" in o:
            i["title"] = one(o, "title")
        body = text_arg(o, "body", "body_file")
        if body is not None:
            i["body"] = body
        add = split_labels(o.get("add_label", []))
        gh_check_labels(db, add)
        i["labels"] += [x for x in add if x not in i["labels"]]
        i["labels"] = [x for x in i["labels"] if x not in split_labels(o.get("remove_label", []))]
        if "milestone" in o:
            i["milestone"] = gh_milestone_ref(db, one(o, "milestone"))
        if "--remove-milestone" in b:
            i["milestone"] = None
        me = lambda xs: [USER if x == "@me" else x for x in xs]
        i["assignees"] += [x for x in me(split_labels(o.get("add_assignee", []))) if x not in i["assignees"]]
        i["assignees"] = [x for x in i["assignees"] if x not in me(split_labels(o.get("remove_assignee", [])))]
        save(db)
        print("https://github.com/fake/repo/issues/%d" % i["number"])
    elif verb == "close":
        i = find_issue(db, pos[0])
        if "comment" in o:
            add_comment(db, i, one(o, "comment"))
        close_issue(i, one(o, "reason", "completed"))
        save(db)
    elif verb == "reopen":
        i = find_issue(db, pos[0])
        i.update(state="open", state_reason=None, closed_at=None)
        save(db)
    elif verb == "comment":
        i = find_issue(db, pos[0])
        add_comment(db, i, text_arg(o, "body", "body_file") or "")
        save(db)
        print("https://github.com/fake/repo/issues/%d#issuecomment-1" % i["number"])
    else:
        raise Fail("unsupported", 2)


def gh_label_cmd(db, args):
    o, b, pos = parse(args[1:], {"--color": "c", "-c": "c", "--description": "d", "-d": "d", "--json": "json",
                                 "--limit": "limit", "-L": "limit"},
                      ("--force", "-f"))
    if args[0] == "create":
        name = pos[0]
        if name in db["labels"]:
            if not ({"--force", "-f"} & b):
                raise Fail('label with name "%s" already exists; use `--force` to update its color and description' % name)
        else:
            db["labels"].append(name)
            save(db)
    elif args[0] == "list":
        emit([{"name": n} for n in db["labels"]])
    else:
        raise Fail("unsupported", 2)


def parse_api(args, extra_flags):
    flags = {"-X": "method", "--method": "method", "-f": "field", "--raw-field": "field",
             "-F": "field", "--field": "field"}
    flags.update(extra_flags)
    o, b, pos = parse(args, flags, ("--paginate",))
    fields = {}
    for kv in o.get("field", []):
        k, _, v = kv.partition("=")
        fields[k] = v
    path = pos[0].split("?")[0].lstrip("/")
    return path, one(o, "method"), fields, o


def ms_by_number(db, n):
    for m in db["milestones"]:
        if m["number"] == int(n):
            return m
    raise Fail("404 Not Found")


def gh_api(db, args):
    path, method, fields, _ = parse_api(args, {})
    method = (method or ("POST" if fields else "GET")).upper()
    m = re.match(r"^repos/[^/]+/[^/]+/(.+)$", path)
    if not m:
        raise Fail("unsupported", 2)
    rest = m.group(1)
    ci = re.match(r"^commits/[0-9a-f]+/(check-suites|check-runs|status)$", rest)
    if ci and method == "GET":
        # FAKE_CI: pass, fail, pending, or unset (CI never started).
        state = os.environ.get("FAKE_CI", "")
        if ci.group(1) == "check-suites":
            return emit({"total_count": 0, "check_suites": []})
        if ci.group(1) == "status":
            return emit({"state": "pending", "statuses": []})
        runs = {"pass": [{"name": "ci/test", "status": "completed", "conclusion": "success"}],
                "fail": [{"name": "ci/test", "status": "completed", "conclusion": "failure"}],
                "pending": [{"name": "ci/test", "status": "in_progress", "conclusion": None}]}.get(state, [])
        return emit({"total_count": len(runs), "check_runs": runs})
    gm = lambda x: {"number": x["number"], "title": x["title"],
                    "description": x["description"], "state": x["state"]}
    if rest == "milestones" and method == "GET":
        emit([gm(x) for x in db["milestones"]])
    elif rest == "milestones" and method == "POST":
        x = new_milestone(db, fields.get("title", ""), fields.get("description"), fields.get("state", "open"))
        save(db)
        emit(gm(x))
    elif re.match(r"^milestones/\d+$", rest) and method == "PATCH":
        x = ms_by_number(db, rest.split("/")[1])
        for k in ("title", "description", "state"):
            if k in fields:
                x[k] = fields[k]
        save(db)
        emit(gm(x))
    elif re.match(r"^issues/\d+/comments$", rest) and method == "GET":
        i = thread(db, rest.split("/")[1])
        emit([{"id": c["id"], "body": c["body"], "user": {"login": c["author"]}} for c in i["comments"]])
    elif re.match(r"^issues/\d+/comments$", rest) and method == "POST":
        c = add_comment(db, thread(db, rest.split("/")[1]), fields.get("body", ""))
        save(db)
        emit({"id": c["id"], "body": c["body"], "user": {"login": c["author"]}})
    elif re.match(r"^issues/comments/\d+$", rest) and method == "GET":
        cid = int(rest.split("/")[2])
        for num, comments in threads(db):
            for c in comments:
                if c["id"] == cid:
                    emit({"id": c["id"], "body": c["body"], "user": {"login": c["author"]},
                          "html_url": "https://github.com/fake/repo/issues/%d#issuecomment-%d" % (num, cid)})
                    return
        raise Fail("404 Not Found")
    elif re.match(r"^issues/comments/\d+$", rest) and method == "DELETE":
        cid = int(rest.split("/")[2])
        for _, comments in threads(db):
            for c in comments:
                if c["id"] == cid:
                    comments.remove(c)
                    save(db)
                    return
        raise Fail("404 Not Found")
    elif re.match(r"^issues/comments/\d+$", rest) and method == "PATCH":
        cid = int(rest.split("/")[2])
        for _, comments in threads(db):
            for c in comments:
                if c["id"] == cid:
                    c["body"] = fields.get("body", c["body"])
                    save(db)
                    emit({"id": c["id"], "body": c["body"], "user": {"login": c["author"]}})
                    return
        raise Fail("404 Not Found")
    else:
        raise Fail("unsupported", 2)


def run_gh(db, args):
    if args[:2] == ["auth", "status"]:
        return
    if args and args[0] == "issue" and len(args) > 1:
        return gh_issue_cmd(db, args[1:])
    if args and args[0] == "pr" and len(args) > 1:
        return gh_pr_cmd(db, args[1:])
    if args and args[0] == "label" and len(args) > 1:
        return gh_label_cmd(db, args[1:])
    if args and args[0] == "api":
        return gh_api(db, args[1:])
    raise Fail("unsupported", 2)


# ---------------------------------------------------------------- glab
def gl_issue(i):
    return {
        "iid": i["number"], "title": i["title"], "description": i["body"],
        "labels": list(i["labels"]),
        "milestone": ({"title": i["milestone"][0], "iid": i["milestone"][1]}
                      if i["milestone"] else None),
        "state": "opened" if i["state"] == "open" else "closed",
        "web_url": "https://gitlab.com/fake/repo/-/issues/%d" % i["number"],
        "closed_at": i["closed_at"],
        "assignees": [{"username": a} for a in i["assignees"]],
    }


def gl_milestone_ref(db, title):
    m = find_milestone(db, title)
    if not m:
        raise Fail("milestone '%s' not found" % title)
    return [m["title"], m["number"]]


GL_ISSUE_FLAGS = {
    "--title": "title", "-t": "title", "--description": "desc", "-d": "desc",
    "--label": "label", "-l": "label", "--unlabel": "unlabel", "--milestone": "milestone",
    "-m": "milestone", "--per-page": "per_page", "-P": "per_page", "--output": "output",
    "-O": "output", "--assignee": "assignee", "-a": "assignee", "--message": "message",
}


def gl_issue_cmd(db, args):
    verb, rest = args[0], args[1:]
    flags = dict(GL_ISSUE_FLAGS)
    if verb == "note":
        flags["-m"] = "message"
    o, b, pos = parse(rest, flags, ("--yes", "-y", "--all", "-A", "--closed", "-c", "--comments", "--unassign", "--opened", "-o"))
    if verb == "create":
        labels = split_labels(o.get("label", []))
        for n in labels:
            if n not in db["labels"]:
                db["labels"].append(n)
        ms = gl_milestone_ref(db, one(o, "milestone")) if "milestone" in o else None
        i = new_issue(db, one(o, "title", ""), one(o, "desc"), labels, ms)
        save(db)
        print(gl_issue(i)["web_url"])
    elif verb == "list":
        want = split_labels(o.get("label", []))
        rows = []
        for i in db["issues"]:
            if "--all" in b or "-A" in b:
                pass
            elif "--closed" in b or "-c" in b:
                if i["state"] != "closed":
                    continue
            elif i["state"] != "open":
                continue
            if not all(w in i["labels"] for w in want):
                continue
            if "milestone" in o and not (i["milestone"] and i["milestone"][0] == one(o, "milestone")):
                continue
            rows.append(i)
        rows.sort(key=lambda i: -i["number"])
        rows = rows[:min(int(one(o, "per_page", 30)), 100)]
        emit([gl_issue(i) for i in rows])
    elif verb == "view":
        i = find_issue(db, pos[0])
        out = gl_issue(i)
        if "--comments" in b:
            out["notes"] = [{"id": c["id"], "body": c["body"], "author": {"username": c["author"]}}
                            for c in i["comments"]]
        emit(out)
    elif verb == "update":
        i = find_issue(db, pos[0])
        if "title" in o:
            i["title"] = one(o, "title")
        if "desc" in o:
            i["body"] = one(o, "desc")
        for n in split_labels(o.get("label", [])):
            if n not in db["labels"]:
                db["labels"].append(n)
            if n not in i["labels"]:
                i["labels"].append(n)
        i["labels"] = [x for x in i["labels"] if x not in split_labels(o.get("unlabel", []))]
        if "milestone" in o:
            title = one(o, "milestone")
            i["milestone"] = gl_milestone_ref(db, title) if title else None  # `--milestone ""` clears
        if "--unassign" in b:
            i["assignees"] = []
        # `+user` adds; a bare name replaces the assignees (glab semantics).
        want = split_labels(o.get("assignee", []))
        if want and not all(x.startswith("+") for x in want):
            i["assignees"] = []
        i["assignees"] += [x.lstrip("+") for x in want if x.lstrip("+") not in i["assignees"]]
        save(db)
        print(gl_issue(i)["web_url"])
    elif verb == "close":
        close_issue(find_issue(db, pos[0]))
        save(db)
    elif verb == "reopen":
        find_issue(db, pos[0]).update(state="open", state_reason=None, closed_at=None)
        save(db)
    elif verb == "note":
        add_comment(db, find_issue(db, pos[0]), one(o, "message", ""))
        save(db)
    else:
        raise Fail("unsupported", 2)


def gl_label_cmd(db, args):
    o, b, pos = parse(args[1:], {"--name": "name", "-n": "name", "--color": "c", "-c": "c",
                                 "--description": "d", "-d": "d", "--output": "output", "-O": "output"})
    if args[0] == "create":
        name = one(o, "name")
        if name in db["labels"]:
            raise Fail("Label already exists")
        db["labels"].append(name)
        save(db)
    elif args[0] == "list":
        emit([{"id": k + 1, "name": n} for k, n in enumerate(db["labels"])])
    else:
        raise Fail("unsupported", 2)


def gl_api(db, args):
    path, method, fields, _ = parse_api(args, {"--input": "input"})
    method = (method or ("POST" if fields else "GET")).upper()
    if path == "user" and method == "GET":
        return emit({"id": 1, "username": USER})
    mr = re.match(r"^projects/.+?/merge_requests/(\d+)$", path)
    if mr and method == "GET":
        hit = [p for p in db.get("prs", []) if p.get("mr") and p["number"] == int(mr.group(1))]
        if not hit:
            raise Fail("404 Not Found")
        return emit(gl_mr(hit[-1]))
    m = re.match(r"^projects/.+?/((?:milestones|issues).*)$", path)
    if not m:
        raise Fail("unsupported", 2)
    rest = m.group(1)

    def gm(x):
        return {"id": x["number"] + GL_MILESTONE_ID, "iid": x["number"], "title": x["title"],
                "description": x["description"], "state": "closed" if x["state"] == "closed" else "active"}
    if rest == "milestones" and method == "GET":
        emit([gm(x) for x in db["milestones"]])
    elif rest == "milestones" and method == "POST":
        x = new_milestone(db, fields.get("title", ""), fields.get("description"), "open")
        save(db)
        emit(gm(x))
    elif re.match(r"^milestones/\d+$", rest) and method == "PUT":
        x = ms_by_number(db, int(rest.split("/")[1]) - GL_MILESTONE_ID)
        for k in ("title", "description"):
            if k in fields:
                x[k] = fields[k]
        ev = fields.get("state_event")
        if ev:
            x["state"] = "closed" if ev == "close" else "open"
        save(db)
        emit(gm(x))
    elif re.match(r"^issues/\d+/notes$", rest) and method == "GET":
        i = find_issue(db, rest.split("/")[1])
        emit([{"id": c["id"], "body": c["body"], "author": {"username": c["author"]}, "system": False}
              for c in i["comments"]])
    elif re.match(r"^issues/\d+/notes$", rest) and method == "POST":
        c = add_comment(db, find_issue(db, rest.split("/")[1]), fields.get("body", ""))
        save(db)
        emit({"id": c["id"], "body": c["body"], "author": {"username": c["author"]}, "system": False})
    elif re.match(r"^issues/\d+/notes/\d+$", rest) and method == "DELETE":
        parts = rest.split("/")
        i = find_issue(db, parts[1])
        for c in i["comments"]:
            if c["id"] == int(parts[3]):
                i["comments"].remove(c)
                save(db)
                return
        raise Fail("404 Not found")
    elif re.match(r"^issues/\d+/notes/\d+$", rest) and method == "PUT":
        parts = rest.split("/")
        i = find_issue(db, parts[1])
        for c in i["comments"]:
            if c["id"] == int(parts[3]):
                c["body"] = fields.get("body", c["body"])
                save(db)
                emit({"id": c["id"], "body": c["body"], "author": {"username": c["author"]}})
                return
        raise Fail("404 Not found")
    else:
        raise Fail("unsupported", 2)


def run_glab(db, args):
    if args[:2] == ["auth", "status"]:
        return
    if args and args[0] == "issue" and len(args) > 1:
        return gl_issue_cmd(db, args[1:])
    if args and args[0] == "mr" and len(args) > 1:
        return gl_mr_cmd(db, args[1:])
    if args and args[0] == "label" and len(args) > 1:
        return gl_label_cmd(db, args[1:])
    if args and args[0] == "api":
        return gl_api(db, args[1:])
    raise Fail("unsupported", 2)


# ---------------------------------------------------------------- main
def main():
    tool, args = sys.argv[1], sys.argv[2:]
    line = " ".join(args)
    if os.environ.get("FAKE_TRACKER_LOG"):
        with open(os.environ["FAKE_TRACKER_LOG"], "a") as f:
            f.write(line + "\n")
    sub = os.environ.get("FAKE_TRACKER_FAIL")
    if sub and sub in line:
        sys.stderr.write("fake %s: simulated failure for: %s %s\n" % (tool, line, os.environ.get("FAKE_TRACKER_FAIL_MSG", "")))
        return 1
    try:
        db = load()
        (run_gh if tool == "gh" else run_glab)(db, args)
    except Fail as e:
        if e.code == 2 and str(e) == "unsupported":
            sys.stderr.write("fake %s: unsupported: %s\n" % (tool, line))
        else:
            sys.stderr.write("%s\n" % e)
        return e.code
    return 0


if __name__ == "__main__":
    sys.exit(main())

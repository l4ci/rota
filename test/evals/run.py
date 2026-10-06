#!/usr/bin/env python3
"""Behavioural evals for rota skills (#279). Stdlib only; shells out to `claude -p`.

  python3 test/evals/run.py --check                 offline: validate the case files, no model calls
  python3 test/evals/run.py triggers --model haiku  does the model pick the right skill for a request?
  python3 test/evals/run.py scenarios --model opus  does it act as the skill says in a described situation?

Nothing here runs in the merge gate: every run costs model calls. `--check` is free and
is what CI may run. Scenario answers are simulated (no tools, no repo): the model gets the
SKILL.md text plus the skill's own sibling .md files (each under a header naming it, so text
moved out of SKILL.md stays visible) and a situation, and lists the actions it would take,
scored by regex. Shared files under skills/references/ are NOT loaded.
"""
import argparse, json, re, subprocess, sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
SKILLS = HERE.parent.parent / "skills"
TRIGGER_SYS = (
    "You route user requests to skills. Below is the list of installed skills with their "
    "descriptions. Decide which ONE skill, if any, you would load for the request. Answer with "
    "exactly one line: `SKILL: <name>` or `SKILL: none`. No other text.\n\n"
)
SCENARIO_SYS = (
    "You are a coding agent that has loaded the skill below. Nothing runs for real: you cannot "
    "execute commands, and no output arrives beyond what the situation states. Follow the skill "
    "exactly. Commands you run return the simulated results given in the situation; any command "
    "not covered exits 0 with plausible output. Never stop to wait for output: assume it arrived and "
    "carry on. When the skill asks the user a question, write the ASK line, then continue as if they "
    "picked the Recommended option, unless the skill says to stop. End when the skill says stop or the "
    "cycle ends. One action per line, each starting with one of "
    "`CMD:` (a shell command), `ASK:` (a question to the user, with its options) or `SAY:` "
    "(a message to the user). No other text, no markdown fences.\n\n=== SKILL ===\n"
)


def descriptions():
    out = {}
    for d in sorted(SKILLS.glob("rota-*")):
        text = (d / "SKILL.md").read_text()
        m = re.search(r"^description:\s*(>-?\s*\n(?:[ \t]+.*\n)+|.*)", text, re.M)
        raw = m.group(1).strip()
        out[d.name] = " ".join(raw.lstrip(">-").split()).strip("\"'")
    return out


def skill_text(skill):
    d = SKILLS / skill
    parts = [(d / "SKILL.md").read_text()]
    for f in sorted(d.glob("*.md")):
        if f.name != "SKILL.md":
            parts.append(f"\n\n--- {skill}/{f.name} ---\n\n" + f.read_text())
    return "".join(parts)


def ask(model, system, prompt):
    p = subprocess.run(
        ["claude", "-p", "--model", model, "--system-prompt", system, "--tools", "",
         "--disable-slash-commands", "--setting-sources", "", "--no-session-persistence",
         "--output-format", "json"],
        input=prompt, capture_output=True, text=True, timeout=300)
    if p.returncode != 0:
        return "", 0.0, p.stderr.strip()[:300]
    j = json.loads(p.stdout)
    return j.get("result", ""), j.get("total_cost_usd", 0.0), ""


def pattern(rule):
    return (rule, 1, None) if isinstance(rule, str) else (rule["re"], rule.get("min", 1), rule.get("max"))


def count(rx, text):
    return len(re.findall(rx, text, re.M | re.I))


def score(case, out):
    fails = []
    for rule in case["must"]:
        rx, lo, _ = pattern(rule)
        if count(rx, out) < lo:
            fails.append(f"missing /{rx}/" + (f" x{lo}" if lo > 1 else ""))
    for rule in case["mustNot"]:
        rx, _, hi = pattern(rule)
        n = count(rx, out)
        if (hi is None and n) or (hi is not None and n > hi):
            fails.append(f"forbidden /{rx}/ found")
    pos = 0
    for rx in case["order"]:
        m = re.search(rx, out[pos:], re.M | re.I)
        if not m:
            fails.append(f"out of order /{rx}/")
            break
        pos += m.end()
    return fails


def load_scenarios():
    cases = []
    for f in sorted((HERE / "scenarios").glob("*.json")):
        d = json.loads(f.read_text())
        cases += [dict(c, skill=d["skill"]) for c in d["scenarios"]]
    return cases


def check():
    errs, skills = [], descriptions()
    trig = json.loads((HERE / "triggers.json").read_text())["cases"]
    for s in skills:
        kinds = {c["expect"] for c in trig if c["skill"] == s}
        if kinds != {"load", "skip"}:
            errs.append(f"triggers.json: {s} needs one load and one skip case, has {sorted(kinds)}")
    for c in trig:
        if c["skill"] not in skills:
            errs.append(f"triggers.json: unknown skill {c['skill']}")
        if c.get("better") and c["better"] not in skills:
            errs.append(f"triggers.json: unknown 'better' skill {c['better']}")
    scen = load_scenarios()
    per = {}
    for c in scen:
        per[c["skill"]] = per.get(c["skill"], 0) + 1
        if c["skill"] not in skills:
            errs.append(f"{c['id']}: unknown skill {c['skill']}")
        for k in ("id", "query", "situation", "expected"):
            if not c.get(k):
                errs.append(f"{c.get('id')}: empty {k}")
        if not (c["must"] or c["mustNot"] or c["order"]):
            errs.append(f"{c['id']}: no assertions")
        for rule in c["must"] + c["mustNot"] + c["order"]:
            try:
                re.compile(pattern(rule)[0] if not isinstance(rule, str) else rule)
            except re.error as e:
                errs.append(f"{c['id']}: bad regex {rule!r}: {e}")
    for s in ("rota-work", "rota-ship", "rota-capture"):
        if per.get(s, 0) < 3:
            errs.append(f"{s}: needs 3+ scenarios, has {per.get(s, 0)}")
    for e in errs:
        print("evals check:", e, file=sys.stderr)
    print(f"evals check: {len(trig)} trigger cases, {len(scen)} scenarios, {len(errs)} problems")
    return 1 if errs else 0


def run_triggers(model, workers):
    skills = descriptions()
    listing = "\n".join(f"- {n}: {d}" for n, d in skills.items())
    cases = json.loads((HERE / "triggers.json").read_text())["cases"]

    def one(c):
        out, cost, err = ask(model, TRIGGER_SYS + listing, f"Request: {c['request']}")
        m = re.search(r"SKILL:\s*([\w-]+)", out)
        got = m.group(1) if m else (err or out[:80])
        ok = (got == c["skill"]) if c["expect"] == "load" else (got != c["skill"])
        return dict(id=c["id"], kind="trigger", expect=c["expect"], got=got, better=c.get("better"),
                    ok=ok, cost=cost, fails=[] if ok else [f"expected {c['expect']} {c['skill']}, got {got}"])
    with ThreadPoolExecutor(workers) as ex:
        return list(ex.map(one, cases))


def run_scenarios(model, workers, only):
    cases = [c for c in load_scenarios() if not only or c["id"] in only or c["skill"] in only]

    def one(c):
        prompt = f"Situation: {c['situation']}\n\nThe user typed: {c['query']}\n\nYour next actions:"
        out, cost, err = ask(model, SCENARIO_SYS + skill_text(c["skill"]), prompt)
        fails = score(c, out) if not err else [f"call failed: {err}"]
        return dict(id=c["id"], kind="scenario", ok=not fails, fails=fails, cost=cost, output=out)
    with ThreadPoolExecutor(workers) as ex:
        return list(ex.map(one, cases))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("mode", nargs="?", choices=["triggers", "scenarios"])
    ap.add_argument("--check", action="store_true", help="validate case files offline")
    ap.add_argument("--model", default="sonnet", help="haiku | sonnet | opus (any `claude --model` value)")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--only", nargs="*", help="scenario ids or skill names")
    ap.add_argument("--out", help="write full JSON results here")
    a = ap.parse_args()
    if a.check:
        return check()
    if not a.mode:
        ap.error("pick triggers, scenarios or --check")
    res = run_triggers(a.model, a.workers) if a.mode == "triggers" else run_scenarios(a.model, a.workers, a.only)
    for r in res:
        print(("PASS " if r["ok"] else "FAIL ") + r["id"] + ("" if r["ok"] else "  " + "; ".join(r["fails"])))
    passed = sum(r["ok"] for r in res)
    print(f"{a.mode} on {a.model}: {passed}/{len(res)} passed, ${sum(r['cost'] for r in res):.2f}")
    if a.out:
        Path(a.out).write_text(json.dumps(dict(model=a.model, mode=a.mode, results=res), indent=2) + "\n")
    return 0 if passed == len(res) else 1


if __name__ == "__main__":
    sys.exit(main())

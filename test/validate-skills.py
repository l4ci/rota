"""
validate-skills.py — static schema validator for rota SKILL.md files,
plus the Agent Skills spec frontmatter lint (E2, #69) and the prose-contract
lint (PROSE_RULES, #173).
Stdlib only. Exit 0 on all-pass, exit 1 on any failure, exit 2 on unexpected error.
"""

import json
import os
import re
import sys
from pathlib import Path


def parse_frontmatter(text):
    """Return (dict_of_keys, post_frontmatter_text) or (None, text) if no frontmatter."""
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None, text
    end = None
    for i, line in enumerate(lines[1:], start=1):
        if line.strip() == "---":
            end = i
            break
    if end is None:
        return None, text
    fm_lines = lines[1:end]
    post = "\n".join(lines[end + 1:])
    keys = {}
    for line in fm_lines:
        m = re.match(r'^(\w[\w-]*):\s*(.*)', line)
        if m:
            k, v = m.group(1), m.group(2).strip()
            # strip surrounding quotes if present
            if (v.startswith('"') and v.endswith('"')) or (v.startswith("'") and v.endswith("'")):
                v = v[1:-1]
            keys[k] = v
    return keys, post


def check_frontmatter(path, text, issues):
    fm, _ = parse_frontmatter(text)
    if fm is None:
        issues.append(f"{path}: no frontmatter block found")
        return
    for key in ("name", "description"):
        if key not in fm or not fm[key]:
            issues.append(f"{path}: frontmatter missing required key '{key}'")


# Agent Skills spec frontmatter (E2, #69; https://agentskills.io/specification).
# Codex and Claude Code read the same SKILL.md, and a strict spec validator
# rejects any key outside SPEC_KEYS. The two sets below excuse a violation a
# slice has not fixed yet (empty since E2 fixed them all). An entry with nothing
# left to excuse fails the check, so the sets can only shrink.
SPEC_KEYS = {"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
NAME_RE = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")
PENDING_KEYS = set()
PENDING_LONG = set()
# A description says when to use the skill, not how it works: a long one
# summarises the workflow and agents follow it instead of reading the skill
# (#246, references/authoring-conventions.md). The spec's 1024 is a ceiling, not a target.
DESC_CAP = 350
# The description is injected into the system prompt, so it is written in the
# third person (Anthropic's skill authoring guide, #248). Quoted trigger phrases
# are the user's own words and may say "you" or "I".
SECOND_PERSON = re.compile(r"\b(you|your|I)\b")
# A reference longer than this opens with a "## Contents" section so a partial
# read still shows what the file covers (same guide, #248).
REF_TOC_LINES = 100


def pending_spec():
    # ROTA_SPEC_PENDING (whitespace-separated; a token ending .md is a path for
    # PENDING_LONG, anything else a key for PENDING_KEYS) replaces both sets, so
    # smoke sections can test the check on a fixture tree.
    override = os.environ.get("ROTA_SPEC_PENDING")
    if override is None:
        return PENDING_KEYS, PENDING_LONG
    toks = override.split()
    return {t for t in toks if not t.endswith(".md")}, {t for t in toks if t.endswith(".md")}


BLOCK_SCALARS = {">", "|", ">-", "|-", ">+", "|+"}


def spec_fields(text):
    """Top-level frontmatter as {key: value}, folding indented continuation
    lines into the value; None without a frontmatter block."""
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None
    out, key = {}, None
    for line in lines[1:]:
        if line.strip() == "---":
            return out
        m = re.match(r"^([A-Za-z0-9_-]+):[ \t]*(.*)$", line)
        if m:
            key, val = m.group(1), m.group(2).strip()
            if len(val) > 1 and val[0] == val[-1] and val[0] in "\"'":
                val = val[1:-1]
            out[key] = "" if val in BLOCK_SCALARS else val
        elif key and line[:1] in (" ", "\t"):
            out[key] = (out[key] + " " + line.strip()).strip()
    return None


def plain_scalar_hazards(text):
    """Top-level keys whose unquoted single-line value is not valid YAML: a
    ': ' or ' #' inside it, or a leading indicator. Claude Code reads such a
    value anyway; Codex's strict parser drops the whole skill without a word."""
    lines = text.splitlines()
    bad = []
    for line in lines[1:]:
        if line.strip() == "---":
            break
        m = re.match(r"^([A-Za-z0-9_-]+):[ \t]+(\S.*)$", line)
        if not m:
            continue
        val = m.group(2).rstrip()
        if val[0] in "\"'" or val in BLOCK_SCALARS:
            continue
        if ": " in val or " #" in val or val.endswith(":") or val[0] in "&*!|>%@`[]{},#?-":
            bad.append(m.group(1))
    return bad


def check_spec_frontmatter(path, text, issues):
    pending_keys, pending_long = pending_spec()
    path = Path(path)
    fm = spec_fields(text)
    if fm is None:
        return
    rel = path.as_posix()
    name, desc = fm.get("name", ""), fm.get("description", "")
    if name and name != path.parent.name:
        issues.append(f"{rel}: frontmatter name '{name}' must equal the directory '{path.parent.name}'")
    if name and (len(name) > 64 or not NAME_RE.match(name)):
        issues.append(f"{rel}: name '{name}' must be 1-64 chars of lowercase letters, digits and single hyphens, not starting or ending with one")
    if len(desc) > 1024:
        if rel not in pending_long:
            issues.append(f"{rel}: description is {len(desc)} chars; the spec allows 1024")
    elif rel in pending_long:
        issues.append(f"{rel}: description is within 1024 chars; remove it from PENDING_LONG")
    elif len(desc) > DESC_CAP:
        issues.append(f"{rel}: description is {len(desc)} chars; the cap is {DESC_CAP}. "
                      f"State when to use the skill, not how it works")
    if SECOND_PERSON.search(re.sub(r'"[^"]*"', "", desc)):
        issues.append(f"{rel}: description must be in the third person: say 'the user', not 'you' or 'I', "
                      f"outside quoted trigger phrases")
    for key in plain_scalar_hazards(text):
        issues.append(f"{rel}: frontmatter '{key}' is not valid YAML unquoted (': ', ' #' or a leading indicator); "
                      f"use a folded block (>-) or quote it")
    if len(fm.get("compatibility", "")) > 500:
        issues.append(f"{rel}: compatibility is over 500 chars")
    extra = sorted(set(fm) - SPEC_KEYS)
    bad = [k for k in extra if k not in pending_keys]
    if bad:
        issues.append(f"{rel}: frontmatter key(s) {', '.join(bad)} are not in the Agent Skills spec "
                      f"(allowed: {', '.join(sorted(SPEC_KEYS))})")


def check_pending_spec(skill_files, issues):
    """A pending entry nothing uses any more must be deleted."""
    pending_keys, pending_long = pending_spec()
    used = set()
    for p in skill_files:
        used |= set(spec_fields(Path(p).read_text(encoding="utf-8")) or {}) & pending_keys
    for k in sorted(pending_keys - used):
        issues.append(f"PENDING_KEYS: no SKILL.md uses '{k}' any more; remove it")
    present = {Path(p).as_posix() for p in skill_files}
    for rel in sorted(pending_long - present):
        issues.append(f"{rel}: listed in PENDING_LONG but missing; remove the entry")


def check_references(path, text, issues):
    # Skills cite references/<x>.md; `rota skills install` copies each cited file
    # next to the skill, so in the source tree the link resolves against the
    # skills/references/ dir.
    pattern = re.compile(r'\((references/[^)\s]+\.md)\)')
    for m in pattern.finditer(text):
        target = m.group(1)
        resolved = (Path(path).parent.parent / target).resolve()
        if not resolved.exists():
            issues.append(f"{path}: broken reference '{target}' -> '{resolved}'")


def check_skill_files(path, text, issues):
    # A skill's own sibling files (skills/rota-<name>/<topic>.md, #316) are linked
    # from SKILL.md as `[x](x.md)` and read on a condition. Every such link must
    # resolve, every sibling must be linked, and a sibling does not link another.
    folder = Path(path).parent
    linked = set()
    for m in re.finditer(r'\]\(([A-Za-z0-9][A-Za-z0-9._-]*\.md)\)', text):
        linked.add(m.group(1))
        if not (folder / m.group(1)).is_file():
            issues.append(f"{path}: broken sibling link '{m.group(1)}'")
    for sib in sorted(folder.glob("*.md")):
        if sib.name == "SKILL.md":
            continue
        if sib.name not in linked:
            issues.append(f"{sib.as_posix()}: not linked from {path}")
        body = sib.read_text(encoding="utf-8")
        lines = body.splitlines()
        if len(lines) > REF_TOC_LINES and "## Contents" not in lines[:15]:
            issues.append(f"{sib.as_posix()}: {len(lines)} lines with no '## Contents' section in its first 15 lines")
        others = {s.name for s in folder.glob("*.md") if s != sib}
        for m in re.finditer(r'\]\(([A-Za-z0-9][A-Za-z0-9._-]*\.md)\)', body):
            if m.group(1) in others:
                issues.append(f"{sib.as_posix()}: links sibling '{m.group(1)}'; extracted files do not link each other")


STEP_HEADING = re.compile(r'^#{2,4}\s+((?:Step\s+\d|\d+\.\s).*?)\s*$')
CHECKLIST_LEAD = "Copy this checklist"


def checklist_parts(text):
    # Walk a SKILL.md outside code fences. Returns the step headings (title text,
    # in file order) and the checklist blocks: the fenced blocks that follow a
    # "Copy this checklist" line, each as a list of "- [ ]" line texts with the
    # offset of the first step heading seen so far.
    headings, blocks = [], []
    fence, lead, cur = None, False, None
    for line in text.splitlines():
        m = re.match(r'^(`{3,}|~{3,})', line)
        if fence is None:
            if m:
                fence = m.group(1)
                cur = [] if lead else None
                if cur is not None:
                    blocks.append((len(headings), cur))
                lead = False
                continue
            h = STEP_HEADING.match(line)
            if h:
                headings.append(h.group(1))
            if line.strip():
                lead = line.startswith(CHECKLIST_LEAD)
        else:
            if m and m.group(1)[0] == fence[0] and len(m.group(1)) >= len(fence):
                fence, cur = None, None
                continue
            if cur is not None:
                c = re.match(r'^- \[ \] (.+?)\s*$', line)
                if c:
                    cur.append(c.group(1))
    return headings, blocks


def check_checklist(path, text, issues):
    # #318: a skill with step headings opens its workflow with a copyable
    # checklist (the skills guide's "Workflows and feedback loops" format). One
    # line per step, verbatim heading text (a trailing " (...)" marks a
    # conditional), in heading order; every step heading has a line.
    headings, blocks = checklist_parts(text)
    if not headings:
        return
    if not blocks:
        issues.append(f"{path}: has step headings but no '{CHECKLIST_LEAD}' checklist block")
        return
    if blocks[0][0] != 0:
        issues.append(f"{path}: the first checklist must open the workflow, before the first step heading")
    covered = set()
    for _, lines in blocks:
        last = -1
        for ln in lines:
            idx = next((i for i, h in enumerate(headings) if ln == h or ln.startswith(h + " (")), None)
            if idx is None:
                issues.append(f"{path}: checklist line '{ln}' names no step heading")
                continue
            if idx < last:
                issues.append(f"{path}: checklist line '{ln}' is out of run order")
            last = max(last, idx)
            covered.add(idx)
    for i, h in enumerate(headings):
        if i not in covered:
            issues.append(f"{path}: step heading '{h}' is missing from the checklist")


def check_reference_toc(issues):
    for ref in sorted(Path(".").glob("skills/references/*.md")):
        lines = ref.read_text(encoding="utf-8").splitlines()
        if len(lines) > REF_TOC_LINES and "## Contents" not in lines[:15]:
            issues.append(f"{ref.as_posix()}: {len(lines)} lines with no '## Contents' section in its first 15 lines")


# Prose-contract lint (#173). Skills and docs document rota verbs and flags that
# other skills and tests rely on; these rules pin that wiring (a skill names the
# verb it calls, a documented flag keeps its section, a retired phrase stays
# gone). They replaced the grep blocks that lived in test/sections. Each rule is
# a tuple, built by the helpers below:
#   has(path, text, msg)              path must contain text (re=True: a regex)
#   lacks(path, text, msg)            path must not contain text
#   count_ge(path, text, n, msg)      text appears at least n times
#   only_in(glob, text, names, msg)   exactly these skill dirs contain text
#   paired(glob, trigger, need, msg)  every file with trigger also has need
# A rule on a missing file fails. ROTA_DOCLINT_PROSE=off skips the lint so section
# 94 can run the spec lint on a fixture tree.
def has(path, text, msg, re_=False, flags=0):
    return ("has", path, text, msg, re_, flags)


def lacks(path, text, msg, re_=False, flags=0):
    return ("lacks", path, text, msg, re_, flags)


def count_ge(path, text, n, msg):
    return ("count_ge", path, text, n, msg)


def only_in(glob, text, names, msg):
    return ("only_in", glob, text, names, msg)


def paired(glob, trigger, need, msg):
    return ("paired", glob, trigger, need, msg)


def prose_rules():
    r = []
    sk = lambda n: f"skills/rota-{n}/SKILL.md"
    CALLOUT = "**always manual** — never auto-invoked, regardless of `autonomy.level`"
    # handoff / feature-branch guard calls
    r += [has("skills/rota-work/no-argument-mode.md", "rota status handoff", "must call rota status handoff"),
          has(sk("ship"), "rota git guard feature-branch", "must call rota git guard feature-branch"),
          has(sk("pause"), "rota git guard feature-branch", "must call rota git guard feature-branch")]
    # map / backlog touchpoints
    r += [has(sk("work"), r"rota map stats --cap|rota map index", "has no map touchpoint", True),
          has("skills/rota-work/no-argument-mode.md", "rota backlog stale", "missing the stale-summary call"),
          has(sk("capture"), "Subsystem:", "missing the Subsystem field")]
    # config verbs and the positional-args doc (F09, F78)
    for n in ("ship",):
        r.append(has(sk(n), "rota config set", "missing rota config set call"))
    r += [has("skills/rota-ship/docs-mode.md", r"\| Manual invoke.*after-work.*manual mode",
              "Docs Mode Modes row for manual invocation must reflect after-work in manual mode", True),
          has("skills/rota-ship/docs-mode.md", "Route to the After-work sub-flow", "Docs Mode Step D1 'Already true' branch must route to the after-work sub-flow"),
          has("skills/rota-ship/docs-mode.md", "Manual entry bypasses the gate", "Docs Mode Step D-A1 missing the manual-entry bypass clause"),
          has("docs/reference/config-options.md", "positional", "missing positional-args mention"),
          has("docs/usage/configuration.md", r"positional|<key>=<value>", "missing positional-args mention", True),
          has("docs/usage/configuration.md", "work.dispatch", "does not explain work.dispatch")]
    for key in ("models.orchestrator models.worker work.isolation work.mergeStrategy ship.review learn.verify "
                "refactor.confirmBeforeExecute autonomy.level docs.path docs.autoCreate "
                "docs.afterWork git.baseBranch umbrella.enabled work.dispatch work.workerSlots work.workerCommand").split():
        r.append(has("docs/reference/config-options.md", key, f"does not document {key}"))
    for key in ("work.dispatch", "work.workerSlots", "work.workerCommand"):
        r.append(has("docs/reference/config-options.md", key, f"does not document {key}"))
    # multi-repo flow (M03)
    r += [has("skills/rota-capture/umbrella-tagging.md", r"multiSelect:.*true", "Step 4.6 must declare multiSelect: true for the Repos question", True),
          has("skills/rota-capture/umbrella-tagging.md", "comma-separated list of registered sub-repos", "field-order line must say 'comma-separated list of registered sub-repos'"),
          lacks(sk("capture"), "single name in V1", "must no longer carry the 'single name in V1' qualifier"),
          lacks("skills/rota-capture/umbrella-tagging.md", "single name in V1", "must no longer carry the 'single name in V1' qualifier"),
          has(sk("plan"), "multi-repo items pass the full comma-list", "must explain the multi-repo --repo flow"),
          has("skills/references/work-preview.md", "one line per repo for multi-repo items", "Preview Mode peek must show one Repo line per sub-repo"),
          has("skills/rota-work/umbrella-and-issue-mode.md", "rota git branch", "must reference rota git branch for multi-repo branch creation"),
          has("skills/rota-work/umbrella-and-issue-mode.md", r"rota status add .*--repos", "must reference rota status add --repos for multi-repo status entries", True),
          has("skills/rota-work/umbrella-and-issue-mode.md", "rota repo resolve", "must reference rota repo resolve for multi-repo validation"),
          lacks(sk("work"), "M03 (deferred)", "must no longer say 'M03 (deferred)'"),
          lacks(sk("work"), "wait for M03 multi-repo support", "must no longer say 'wait for M03 multi-repo support'")]
    # worker reset guard, proof path, manual gates
    r += [lacks(sk("work"), r"work\.dispatch.*(tmux|herdr).*(pool|slot)|rota worker (pool|dispatch|poll)", "legacy tmux/herdr dispatch path was removed; rounds own it", True),
          lacks(sk("work"), r"git add (-A|\.)( |$|\n)", "must not stage directory-wide", True, re.M),
          paired("skills/rota-*/SKILL.md", "rota item complete", "rota proof add",
                 "calls rota item complete without a rota proof add path"),
          lacks(sk("capture"), "Import Mode", "Import Mode was removed"),
          has("skills/rota-capture/remove-mode.md", "Step R3", "missing Step R3 (Remove Mode de-tag gate)"),
          has(sk("ship"), "Step 6c", "missing Step 6c (direct-push close gate)"),
          has(sk("ship"), CALLOUT, "missing the manual-gate callout (Step 6c)"),
          has("skills/references/manual-gates.md", r"Step 6c|direct-push close", "missing the rota-ship Step 6c row", True)]
    # F73 subagent-dispatch discipline
    D = "skills/references/subagent-dispatch.md"
    for h in ("When to dispatch", "Small-brief template", "Return-shape contract", "Subagent tiers",
              "Parallel fan-out pattern", "What stays on the orchestrator"):
        r.append(has(D, f"^## {h}", f"section '{h}' missing", True, re.M))
    r += [has(D, "DECISIONS.md", "must cite the .rota/DECISIONS.md worktree-isolation rule"),
          lacks(D, r"TBD|TODO|FIXME|XXX", "contains placeholders", True, re.I),
          has(D, "Skills MUST consult this file", "missing the rule that skills consult the dispatch discipline")]
    # --retro reads the session transcript for evidence
    RR = "skills/references/learn-rare-modes.md"
    r += [has(RR, "~/.codex/sessions/", "--retro Step 1 must locate the Codex transcript"),
          has(RR, "~/.claude/projects/", "--retro Step 1 must locate the Claude Code transcript"),
          has(RR, "must cite transcript evidence", "--retro Step 1 must require transcript citations"),
          has(RR, "No transcript found", "--retro Step 1 must state the no-transcript fallback"),
          has("docs/usage/learning.md", "session transcript", "learning.md must mention the --retro transcript read")]
    return r


def check_prose(issues):
    if os.environ.get("ROTA_DOCLINT_PROSE") == "off":
        return
    cache = {}

    def read(path):
        if path not in cache:
            p = Path(path)
            cache[path] = p.read_text(encoding="utf-8") if p.is_file() else None
        return cache[path]

    def found(text, pat, re_, flags):
        return re.search(pat, text, flags) if re_ else pat in text

    for rule in prose_rules():
        kind = rule[0]
        if kind in ("has", "lacks", "count_ge"):
            path = rule[1]
            text = read(path)
            if text is None:
                issues.append(f"{path}: prose rule target is missing")
                continue
        if kind == "has":
            _, path, pat, msg, re_, flags = rule
            if not found(text, pat, re_, flags):
                issues.append(f"{path}: {msg}")
        elif kind == "lacks":
            _, path, pat, msg, re_, flags = rule
            if found(text, pat, re_, flags):
                issues.append(f"{path}: {msg}")
        elif kind == "count_ge":
            _, path, pat, n, msg = rule
            if text.count(pat) < n:
                issues.append(f"{path}: {msg} (found {text.count(pat)}, need {n})")
        elif kind == "only_in":
            _, glob, pat, names, msg = rule
            got = {p.parent.name for p in sorted(Path(".").glob(glob)) if pat in p.read_text(encoding="utf-8")}
            if got != names:
                issues.append(f"{glob}: {msg}, got {sorted(got)}")
        elif kind == "paired":
            _, glob, trigger, need, msg = rule
            hits = [p for p in sorted(Path(".").glob(glob)) if trigger in p.read_text(encoding="utf-8")]
            if not hits:
                issues.append(f"{glob}: expected at least one file containing '{trigger}'")
            for p in hits:
                if need not in p.read_text(encoding="utf-8"):
                    issues.append(f"{p.as_posix()}: {msg}")


def main():
    issues = []

    skill_files = sorted(Path(".").glob("skills/rota-*/SKILL.md"))

    for skill_path in skill_files:
        text = skill_path.read_text(encoding="utf-8")
        check_frontmatter(skill_path, text, issues)
        check_spec_frontmatter(skill_path, text, issues)
        check_references(skill_path, text, issues)
        check_skill_files(skill_path, text, issues)
        check_checklist(skill_path, text, issues)

    check_pending_spec(skill_files, issues)
    check_reference_toc(issues)
    check_prose(issues)

    n = len(skill_files)
    if issues:
        for line in issues:
            print(line, file=sys.stderr)
        print(f"validate-skills: FAIL ({len(issues)} issues)")
        sys.exit(1)
    else:
        print(f"validate-skills: PASS ({n} SKILL.md files checked)")
        sys.exit(0)


try:
    main()
except Exception as exc:
    print(f"validate-skills: ERROR {exc}", file=sys.stderr)
    sys.exit(2)

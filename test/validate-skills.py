"""
validate-skills.py — static schema validator for rota SKILL.md files,
plus the Agent Skills spec frontmatter lint (E2, #69) and the prose-contract
lint (PROSE_RULES, #173).
Stdlib only. Exit 0 on all-pass, exit 1 on any failure, exit 2 on unexpected error.

Usage: python3 test/validate-skills.py [--root <dir>] [--no-prose]
  --root      tree to validate (default: the current directory)
  --no-prose  skip the prose-contract lint, for fixture trees that lack its targets
"""

import atexit
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
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
# rejects any key outside SPEC_KEYS.
SPEC_KEYS = {"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
NAME_RE = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")
# Claude Code's own frontmatter key, outside the spec. A skill that sets it true
# is manual-only on Claude Code, and must carry agents/openai.yaml with
# policy.allow_implicit_invocation: false so it is manual-only on Codex too (#677).
CLAUDE_KEYS = {"disable-model-invocation"}
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


BLOCK_SCALARS = {">", "|", ">-", "|-", ">+", "|+"}


def spec_fields(text):
    """Top-level frontmatter as {key: value}, folding indented continuation
    lines into the value; None without a frontmatter block. A folded block
    (> or >-) follows YAML and scripts/fmlite.py: lines join with a space and
    a blank line is a newline."""
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None
    out, key, folded, cont = {}, None, False, []

    def flush():
        if key is None or not cont:
            return
        if folded:
            paras, cur = [], []
            for c in cont:
                if c:
                    cur.append(c)
                else:
                    paras.append(" ".join(cur))
                    cur = []
            paras.append(" ".join(cur))
            out[key] = "\n".join(paras).strip("\n")
        else:
            out[key] = (out[key] + " " + " ".join(c for c in cont if c)).strip()

    for line in lines[1:]:
        if line.strip() == "---":
            flush()
            return out
        m = re.match(r"^([A-Za-z0-9_-]+):[ \t]*(.*)$", line)
        if m:
            flush()
            key, val = m.group(1), m.group(2).strip()
            cont, folded = [], val[:1] == ">"
            if len(val) > 1 and val[0] == val[-1] and val[0] in "\"'":
                val = val[1:-1]
            out[key] = "" if val in BLOCK_SCALARS else val
        elif key and (line[:1] in (" ", "\t") or not line.strip()):
            cont.append(line.strip())
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
        issues.append(f"{rel}: description is {len(desc)} chars; the spec allows 1024")
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
    extra = sorted(set(fm) - SPEC_KEYS - CLAUDE_KEYS)
    if extra:
        issues.append(f"{rel}: frontmatter key(s) {', '.join(extra)} are not in the Agent Skills spec "
                      f"(allowed: {', '.join(sorted(SPEC_KEYS))})")


def check_manual_only(path, text, issues):
    """disable-model-invocation: true and agents/openai.yaml go together."""
    fm = spec_fields(text) or {}
    flag = fm.get("disable-model-invocation")
    yaml_path = Path(path).parent / "agents" / "openai.yaml"
    rel = Path(path).as_posix()
    if flag not in (None, "true", "false"):
        issues.append(f"{rel}: disable-model-invocation must be true or false, not '{flag}'")
    manual = flag == "true"
    if not manual:
        if yaml_path.exists():
            issues.append(f"{yaml_path.as_posix()}: skill is model-invocable; remove the file or set disable-model-invocation: true")
        return
    if not yaml_path.is_file():
        issues.append(f"{rel}: disable-model-invocation is true but {yaml_path.as_posix()} is missing")
        return
    # Parse with PyYAML when present; else the one shape rota writes.
    body = yaml_path.read_text(encoding="utf-8")
    try:
        import yaml
        try:
            val = ((yaml.safe_load(body) or {}).get("policy") or {}).get("allow_implicit_invocation")
        except (yaml.YAMLError, AttributeError) as exc:
            issues.append(f"{yaml_path.as_posix()}: does not parse as YAML: {exc}")
            return
    except ImportError:
        m = re.search(r"(?m)^policy:[ \t]*\n[ \t]+allow_implicit_invocation:[ \t]*(true|false)[ \t]*$", body)
        val = None if not m else m.group(1) == "true"
    if val is not False:
        issues.append(f"{yaml_path.as_posix()}: needs policy.allow_implicit_invocation: false")


def check_flag_consistency(skill_files, issues):
    """The flagged set matches authoring-conventions, and no skill chains into a flagged one (#1897)."""
    flagged = {s.parent.name for s in skill_files
               if (spec_fields(s.read_text(encoding="utf-8")) or {}).get("disable-model-invocation") == "true"}
    conv = Path("skills/references/authoring-conventions.md")
    if not conv.is_file():
        listed = flagged  # a minimal tree has no conventions doc: nothing to compare
    else:
        m = re.search(r"The set is (.+?)\. ", conv.read_text(encoding="utf-8"))
        if not m:
            issues.append(f"{conv.as_posix()}: no 'The set is ...' sentence naming the flagged skills")
        listed = set(re.findall(r"`(rota-[a-z]+)`", m.group(1))) if m else set()
    if listed != flagged:
        issues.append(f"{conv.as_posix()}: flagged-skill list {sorted(listed)} differs from the "
                      f"disable-model-invocation flags set {sorted(flagged)}")
    # Another skill's text must not tell a model to run a flagged skill: it cannot. Only
    # imperative clauses are flagged (a command to run it, or a command whose object it is);
    # prose references ("see", "like ... does", tables) pass, and so does an explicit offer
    # for the user to type it. Before the mention, the nearest verb decides: run-ish is a
    # chain, "type"/"offer" is an offer. After it, a trailing "then/else run it" is a chain
    # even behind an offer.
    if not flagged:
        return
    names = "|".join(sorted(flagged))
    mention = re.compile(r"`/?(%s)\b" % names)
    verbs = re.compile(r"(?i)(?<!-)\b(invoke[sd]?|invoking|dispatch(?:es|ed)?|chain(?:s|ed)? into|read and follow|"
                       r"hand(?:s|ed)?(?: \w+){0,3} (?:off )?to|pass(?:es|ed)?(?: \w+){0,3} to|uses?|using|runs?|running|"
                       r"start(?:s|ed)?|launch(?:es|ed)?|call(?:s|ed)?|execut(?:e|es|ed|ing)|"
                       r"(?:switch(?:es|ed)?|delegat(?:e|es|ed)|proceed(?:s|ed)?|go(?:es)?) to|"
                       r"continue[sd]? with|follow(?:s|ed)? up with|follow(?:s|ed)?|"
                       r"type|offer(?:s|ed)?|next step)\b")
    trailing = re.compile(r"(?i)^[^.;!?]*?\b(?:then|else|and|or|just|to)\s+(?:run|invoke|start|launch|use)\s+it\b")
    seen = set()
    root = Path("skills").resolve()
    files = [*Path("skills").glob("*/*.md"), *Path("skills").glob("*/*/*.md"), *Path("skills").glob("*/*/*/*.md")]
    for path in sorted(files):
        real = path.resolve()
        if real in seen:
            continue
        seen.add(real)
        own = real.relative_to(root).parts[0]
        for n, line in enumerate(real.read_text(encoding="utf-8").splitlines(), 1):
            for hit in mention.finditer(line):
                if hit.group(1) == own:
                    continue
                before = line[:hit.start()]
                clause = re.split(r"[.;!?](?:\s|$)|[()\u2014]|\*\"", before)[-1]
                prior = [v.group(1).lower() for v in verbs.finditer(clause)]
                # "Next step: `/x`" has its colon split off the clause: look at the whole line head.
                if re.search(r"(?i)\bnext step\W*$", before):
                    prior = ["next step"]
                # A sentence-initial bare "Then `/x`" is a command with its verb elided.
                if not prior and re.fullmatch(r"(?i)\s*(?:and\s+)?then\s*", clause):
                    prior = ["then"]
                offer = bool(prior) and prior[-1] in ("type", "offer", "offers", "offered")
                chained = bool(prior) and not offer
                if offer and trailing.search(line[hit.end():]):
                    chained = True
                if chained:
                    issues.append(f"{real.relative_to(root.parent).as_posix()}:{n}: chains into flagged skill {hit.group(1)} "
                                  "(manual-only; tell the user to type it instead)")


def supported_skill_file(rel):
    # Mirrors internal/skills.SupportedFile, the owner of the skill-file layout:
    # flat markdown plus agents/*.yaml. layout_test.go pins both to the same verdicts.
    parts = rel.split("/")
    if len(parts) == 1:
        return rel.endswith(".md")
    return len(parts) == 2 and parts[0] == "agents" and rel.endswith(".yaml")


def check_layout(skills_dir, issues):
    # The binary embeds only the supported layout, so a nested file would work
    # from a source checkout or the Codex plugin yet never reach rota skills get.
    def files(d):
        for f in sorted(d.iterdir()):
            if f.is_dir() and not f.is_symlink():
                yield from files(f)
            else:
                yield f

    for skill in sorted(Path(skills_dir).glob("rota-*")):
        for f in files(skill):
            rel = f.relative_to(skill).as_posix()
            if rel == "references" and f.is_symlink():
                continue
            if not supported_skill_file(rel):
                issues.append(f"{f.as_posix()}: unsupported skill content; skills ship flat markdown and agents/*.yaml only (the binary does not package it)")


_ROTA = None


def rota_bin():
    # The binary is the owner of "what is a relative link" (skills.Links), so the
    # validator asks it instead of keeping a regex. ROTA_BIN wins; else build one
    # from this checkout, once.
    global _ROTA
    if _ROTA is None:
        _ROTA = os.environ.get("ROTA_BIN") or ""
        if not _ROTA:
            repo = Path(__file__).resolve().parent.parent
            build_dir = tempfile.mkdtemp(prefix="validate-skills-")
            atexit.register(shutil.rmtree, build_dir, ignore_errors=True)
            out = os.path.join(build_dir, "rota")
            subprocess.run(["go", "build", "-o", out, "./cmd/rota"], cwd=repo, check=True)
            _ROTA = out
    return _ROTA


def skill_links(*paths):
    """Map each markdown path to its relative link targets, via `rota skills links`."""
    out = subprocess.run([rota_bin(), "skills", "links", "--json", *map(str, paths)],
                         capture_output=True, text=True, check=True).stdout
    return {f["file"]: f["links"] for f in json.loads(out)["data"]["files"]}


SIBLING = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]*\.md')


def check_references(path, text, issues):
    # Skills cite references/<x>.md; `rota skills install` copies each cited file
    # next to the skill, so in the source tree the link resolves against the
    # skills/references/ dir.
    for target in skill_links(path)[str(path)]:
        if not target.startswith("references/") or not target.endswith(".md"):
            continue
        resolved = (Path(path).parent.parent / target).resolve()
        if not resolved.exists():
            issues.append(f"{path}: broken reference '{target}' -> '{resolved}'")


def check_skill_files(path, text, issues):
    # A skill's own sibling files (skills/rota-<name>/<topic>.md, #316) are linked
    # from SKILL.md as `[x](x.md)` and read on a condition. Every such link must
    # resolve, every sibling must be linked, and a sibling does not link another.
    folder = Path(path).parent
    sibs = sorted(s for s in folder.glob("*.md") if s.name != "SKILL.md")
    links = skill_links(path, *sibs)
    linked = set()
    for target in links[str(path)]:
        if SIBLING.fullmatch(target):
            linked.add(target)
            if not (folder / target).is_file():
                issues.append(f"{path}: broken sibling link '{target}'")
        elif "/" in target and not target.startswith(("references/", "../")):
            issues.append(f"{path}: link '{target}' points into a subdirectory; the binary packages flat sibling markdown only")
    for sib in sibs:
        if sib.name not in linked:
            issues.append(f"{sib.as_posix()}: not linked from {path}")
        body = sib.read_text(encoding="utf-8")
        lines = body.splitlines()
        if len(lines) > REF_TOC_LINES and "## Contents" not in lines[:15]:
            issues.append(f"{sib.as_posix()}: {len(lines)} lines with no '## Contents' section in its first 15 lines")
        others = {s.name for s in sibs if s != sib}
        for target in links[str(sib)]:
            if target in others:
                issues.append(f"{sib.as_posix()}: links sibling '{target}'; extracted files do not link each other")


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
# A rule on a missing file fails. --no-prose skips the lint so section
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
    r += [has("skills/rota-docs/SKILL.md", r"\| Manual invoke.*after-work.*manual mode",
              "Docs Mode Modes row for manual invocation must reflect after-work in manual mode", True),
          has("skills/rota-docs/SKILL.md", "Route to the After-work sub-flow", "Docs Mode Step D1 'Already true' branch must route to the after-work sub-flow"),
          has("skills/rota-docs/SKILL.md", "Manual entry bypasses the gate", "Docs Mode Step D-A1 missing the manual-entry bypass clause"),
          has("docs/reference/config-options.md", "positional", "missing positional-args mention"),
          has("docs/usage/configuration.md", r"positional|<key>=<value>", "missing positional-args mention", True),
          has("docs/usage/configuration.md", "round.dispatch", "does not explain round.dispatch")]
    for key in ("orchestrator.model round.tiers.claude.standard work.isolation work.mergeStrategy ship.review learn.verify "
                "refactor.confirmBeforeExecute autonomy.level docs.path docs.autoCreate "
                "docs.afterWork git.baseBranch umbrella.enabled round.dispatch round.slots round.commands").split():
        r.append(has("docs/reference/config-options.md", key, f"does not document {key}"))
    for key in ("round.dispatch", "round.slots", "round.commands"):
        r.append(has("docs/reference/config-options.md", key, f"does not document {key}"))
    # multi-repo flow (M03)
    r += [has("skills/rota-capture/umbrella-tagging.md", "**Selection:** multiple", "Step 4.6 must allow multiple selections for the Repos question"),
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
          has("skills/rota-capture/triage-mode.md", "Step T5", "missing Step T5 (Triage Mode outcome)"),
          has("skills/rota-capture/triage-mode.md", CALLOUT, "missing the manual-gate callout (Step T5)"),
          has("skills/references/manual-gates.md", r"/rota-capture.*Step T5", "missing the rota-capture Step T5 row", True),
          has("skills/rota-capture/triage-mode.md", "rota decisions query", "Triage Mode must check prior decisions"),
          has(sk("capture"), "triage-mode.md", "SKILL.md does not route --triage to triage-mode.md"),
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
    # reviewer brief: export with git archive, never touch the repo's worktrees (#1293)
    RB = "skills/rota-review/reviewer-briefs.md"
    r += [has(RB, "rota review prepare <sha>", "reviewer brief must delegate preparation to the supported verb"),
          has(RB, "Never run `git worktree add`, `git worktree remove`, `git checkout` or `git switch` in the repo",
              "reviewer brief must forbid worktree add/remove, checkout and switch in the repo"),
          has(RB, "If preparation failed or the workspace is unavailable, stop and report it. No fallback", "reviewer brief must refuse unavailable workspaces"),
          has(RB, "scratch cwd is not an OS sandbox", "reviewer brief must retain absolute-path access limitations"),
          has(RB, "set `ROTA_BIN`", "reviewer brief must say to set ROTA_BIN for smoke sections")]
    # --retro reads the session transcript for evidence
    RR = "skills/references/learn-rare-modes.md"
    r += [has(RR, "~/.codex/sessions/", "--retro Step 1 must locate the Codex transcript"),
          has(RR, "~/.claude/projects/", "--retro Step 1 must locate the Claude Code transcript"),
          has(RR, "must cite transcript evidence", "--retro Step 1 must require transcript citations"),
          has(RR, "No transcript found", "--retro Step 1 must state the no-transcript fallback"),
          has("docs/usage/learning.md", "session transcript", "learning.md must mention the --retro transcript read")]
    return r


def check_prose(issues):
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


def main(prose=True):
    issues = []

    skill_files = sorted(Path(".").glob("skills/rota-*/SKILL.md"))
    check_layout("skills", issues)

    # The plugin's own skills (rota-install) ship outside skills/ but get the same per-file checks.
    plugin_files = sorted(Path(".").glob("plugin/skills/*/SKILL.md"))

    for skill_path in [*skill_files, *plugin_files]:
        text = skill_path.read_text(encoding="utf-8")
        check_frontmatter(skill_path, text, issues)
        check_spec_frontmatter(skill_path, text, issues)
        check_manual_only(skill_path, text, issues)
        check_references(skill_path, text, issues)
        check_skill_files(skill_path, text, issues)
        check_checklist(skill_path, text, issues)

    check_flag_consistency(skill_files, issues)
    check_reference_toc(issues)
    if prose:
        check_prose(issues)

    n = len(skill_files) + len(plugin_files)
    if issues:
        for line in issues:
            print(line, file=sys.stderr)
        print(f"validate-skills: FAIL ({len(issues)} issues)")
        sys.exit(1)
    else:
        print(f"validate-skills: PASS ({n} SKILL.md files checked)")
        sys.exit(0)


def layout_only(skills_dir):
    issues = []
    check_layout(skills_dir, issues)
    for line in issues:
        print(line, file=sys.stderr)
    sys.exit(1 if issues else 0)


def print_fields(path):
    """--fields <SKILL.md>: the parsed top-level fields as JSON (the seam test compares them)."""
    print(json.dumps(spec_fields(Path(path).read_text(encoding="utf-8"))))
    sys.exit(0)


def parse_args(argv):
    """Return (root, prose); --root changes into the tree, so every check keeps its relative paths."""
    root, prose, i = None, True, 0
    while i < len(argv):
        if argv[i] == "--root" and i + 1 < len(argv):
            root, i = argv[i + 1], i + 2
        elif argv[i] == "--no-prose":
            prose, i = False, i + 1
        else:
            raise SystemExit(f"validate-skills: unknown argument {argv[i]!r}\n{__doc__.split('Usage: ')[1].strip()}")
    return root, prose


try:
    if len(sys.argv) == 3 and sys.argv[1] == "--layout":
        layout_only(sys.argv[2])
    if len(sys.argv) == 3 and sys.argv[1] == "--fields":
        print_fields(sys.argv[2])
    root, prose = parse_args(sys.argv[1:])
    if root is not None:
        os.chdir(root)
    main(prose)
except Exception as exc:
    print(f"validate-skills: ERROR {exc}", file=sys.stderr)
    sys.exit(2)

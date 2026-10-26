#!/usr/bin/env python3
# Regenerates the rota docs section of the Jekyll homepage from docs/: pages
# under <site>/rota/docs/ (front matter, rewritten links, kramdown callouts)
# and the sidebar data <site>/_data/rota/nav.yml, parsed from docs/README.md.
# It also writes _data/rota/skills.json and chains.json from the docs/skills/
# front matter, for the skill cards and the landing page's explorer.
# Everything it writes is generated; docs/contributing/ is left out. Only those
# paths in the site are deleted and rewritten. Runs are idempotent.
# Usage: python3 scripts/site-sync.py <site-root>
import json, os, re, shutil, sys, posixpath

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fmlite  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DOCS = os.path.join(REPO, "docs")
GH = "https://github.com/l4ci/rota"
BASE = "/rota/docs/"
warnings = []


def warn(msg):
    warnings.append(msg)
    print("warning: " + msg, file=sys.stderr)


def walk():
    md, other = [], []
    for d, dirs, files in os.walk(DOCS):
        dirs.sort()
        rel_d = os.path.relpath(d, DOCS).replace(os.sep, "/")
        if rel_d == "contributing" or rel_d.startswith("contributing/"):
            dirs[:] = []
            continue
        for f in sorted(files):
            rel = f if rel_d == "." else rel_d + "/" + f
            (md if f.endswith(".md") else other).append(rel)
    return sorted(md), sorted(other)


def permalink(rel):
    p = rel[:-3]
    if p == "README":
        return BASE
    if p.endswith("/README"):
        return BASE + p[: -len("README")]
    return BASE + p + "/"


def out_rel(rel):
    return rel[:-9] + "index.md" if rel.endswith("README.md") else rel


MD, OTHER = walk()
MDSET, OTHERSET = set(MD), set(OTHER)


def resolve(target, src_rel):
    """Return the rewritten target, or None to leave it alone."""
    if not target or target.startswith("#") or target.startswith("/") or target.startswith("{"):
        return None
    if re.match(r"^[a-zA-Z][a-zA-Z0-9+.-]*:", target):
        return None
    m = re.match(r"^([^#?]*)((?:[#?].*)?)$", target)
    path, frag = m.group(1), m.group(2)
    if not path:
        return None
    repo_rel = posixpath.normpath(posixpath.join("docs", posixpath.dirname(src_rel), path))
    full = os.path.join(REPO, repo_rel)
    in_docs = repo_rel.startswith("docs/") and not (
        repo_rel == "docs/contributing" or repo_rel.startswith("docs/contributing/"))
    drel = repo_rel[5:] if in_docs else None
    if in_docs and drel in MDSET:
        return permalink(drel) + frag
    if in_docs and drel in OTHERSET:
        return BASE + drel + frag
    if in_docs and os.path.isdir(full) and (drel + "/README.md") in MDSET:
        return permalink(drel + "/README.md") + frag
    if not os.path.exists(full):
        warn("%s: link target does not exist: %s" % (src_rel, target))
        return None
    kind = "tree" if os.path.isdir(full) else "blob"
    return "%s/%s/main/%s%s" % (GH, kind, repo_rel, frag)


FENCE = re.compile(r"^\s*(`{3,}|~{3,})")
CODE = re.compile(r"(`+)(.+?)(?<!`)\1(?!`)")
INLINE = re.compile(r"(\]\(\s*<?)([^)\s>]+)(>?(?:\s+\"[^\"]*\")?\s*\))")
REFDEF = re.compile(r"^(\s{0,3}\[[^\]]+\]:\s*)(\S+)")
HTMLATTR = re.compile(r"""((?:href|src)=")([^"]*)(")""")


def rewrite_line(line, src_rel):
    codes = []

    def mask(m):
        codes.append(m.group(0))
        return "\x00%d\x00" % (len(codes) - 1)

    s = CODE.sub(mask, line)

    def sub(m):
        r = resolve(m.group(2), src_rel)
        return m.group(1) + (r if r else m.group(2)) + m.group(3)

    s = INLINE.sub(sub, s)
    s = HTMLATTR.sub(sub, s)
    m = REFDEF.match(s)
    if m:
        r = resolve(m.group(2), src_rel)
        if r:
            s = m.group(1) + r + s[m.end():]
    return re.sub(r"\x00(\d+)\x00", lambda m: codes[int(m.group(1))], s)


ALERT = re.compile(r"^>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*(.*)$")


def convert_alerts(lines):
    out, i, fence = [], 0, None
    while i < len(lines):
        ln = lines[i]
        fm = FENCE.match(ln)
        if fm:
            if fence is None:
                fence = fm.group(1)[0]
            elif fm.group(1)[0] == fence:
                fence = None
        if fence is None and not fm and ln.startswith(">"):
            j = i
            while j < len(lines) and lines[j].startswith(">"):
                j += 1
            block = lines[i:j]
            am = ALERT.match(block[0])
            if am:
                if am.group(2):
                    block[0] = "> " + am.group(2)
                else:
                    block = block[1:]
                block.append("{: .callout .callout-%s}" % am.group(1).lower())
            out.extend(block)
            i = j
            continue
        out.append(ln)
        i += 1
    return out


def plain(s):
    s = re.sub(r"!\[([^\]]*)\]\([^)]*\)", r"\1", s)
    s = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", s)
    s = re.sub(r"\[([^\]]*)\]\[[^\]]*\]", r"\1", s)
    s = re.sub(r"<[^>]+>", "", s)
    s = s.replace("`", "")
    s = re.sub(r"(\*\*|__)(.+?)\1", r"\2", s)
    s = re.sub(r"(?<![\w*])([*_])(.+?)\1(?![\w*])", r"\2", s)
    return re.sub(r"\s+", " ", s).strip()


def describe(lines):
    para = []
    for ln in lines:
        t = ln.strip()
        if not t:
            if para:
                break
            continue
        if para:
            para.append(t)
        elif not re.match(r"^(#|>|[-*+] |\d+[.)] |\||```|~~~|<|\{|!\[)", t):
            para.append(t)
        else:
            # skip non-prose block (until blank line)
            para = []
            continue
    d = plain(" ".join(para))
    if len(d) > 160:
        d = d[:159]
        d = d[: d.rfind(" ")] if " " in d else d
        d = d.rstrip(",;:.- ") + "…"
    return d


def q(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def convert(rel):
    with open(os.path.join(DOCS, rel), encoding="utf-8") as f:
        raw = f.read()
    fmd = {}
    if raw.startswith("---\n"):
        fmd, raw = fmlite.load_front_matter(os.path.join(DOCS, rel), raw)
    lines = raw.split("\n")
    lines = convert_alerts(lines)
    title, h1, fence = rel, None, None
    for i, ln in enumerate(lines):
        fm = FENCE.match(ln)
        if fm:
            fence = None if fence == fm.group(1)[0] else (fence or fm.group(1)[0])
        if fence is None and not fm and re.match(r"^# ", ln):
            title, h1 = plain(ln[2:].rstrip("# ").strip()), i
            break
    rest = lines[h1 + 1:] if h1 is not None else lines
    desc = describe(rest)
    body, fence = [], None
    for ln in rest:
        fm = FENCE.match(ln)
        if fm:
            fence = None if fence == fm.group(1)[0] else (fence or fm.group(1)[0])
            body.append(ln)
        elif fence is not None:
            body.append(ln)
        else:
            body.append(rewrite_line(ln, rel))
    text = "\n".join(body).strip("\n")
    extra = ""
    if fmd.get("skill"):
        desc = fmd.get("summary", desc)
        extra = "skill: %s\n" % fmd["skill"]
    return ("---\nlayout: rota/doc\ntitle: %s\ndescription: %s\npermalink: %s\nsource: docs/%s\n%s---\n"
            "{%% raw %%}\n%s\n{%% endraw %%}\n") % (q(title), q(desc), permalink(rel), rel, extra, text)


LANES = [("capture", "Capture"), ("plan", "Plan"), ("execute", "Execute"), ("ship", "Ship"),
         ("persist", "Persist"), ("rounds", "Rounds"), ("maintenance", "Maintenance")]


def skill_data():
    """Skill cards and chains for the site, from docs/skills/ plus each SKILL.md description."""
    lane_ix = {k: i for i, (k, _) in enumerate(LANES)}
    lane_name = dict(LANES)
    skills = []
    for rel in MD:
        if not re.match(r"^skills/rota-[a-z-]+\.md$", rel):
            continue
        fm, _ = fmlite.load_front_matter(os.path.join(DOCS, rel))
        name = fm["skill"]
        sk, _ = fmlite.load_front_matter(os.path.join(REPO, "skills", name, "SKILL.md"))
        fm["when"] = sk.get("description", "")
        fm["lane_name"] = lane_name[fm["lane"]]
        fm["url"] = permalink(rel)
        ex = fm.get("example") or {}
        if ex.get("source"):
            ex["source_url"] = resolve(ex["source"], "README.md")
        skills.append(fm)
    skills.sort(key=lambda d: (lane_ix[d["lane"]], d["order"]))
    idx, _ = fmlite.load_front_matter(os.path.join(DOCS, "skills", "README.md"))
    chains = idx.get("chains", [])
    for c in chains:
        c["walkthrough_url"] = resolve(c["walkthrough"], "README.md") if c.get("walkthrough") else None
    return skills, chains


def parse_nav():
    groups = [("Start", [("Overview", BASE, "")])]
    cur = None
    item = re.compile(r"^- \[(.+?)\]\(([^)\s]+)\)\s*[:.]\s*(.*)$")
    with open(os.path.join(DOCS, "README.md"), encoding="utf-8") as f:
        for ln in f.read().split("\n"):
            if ln.startswith("### "):
                name = re.sub(r"^[^\w]+", "", ln[4:].strip())
                cur = None if name.lower() == "contributing" else (name, [])
                if cur:
                    groups.append(cur)
                continue
            m = item.match(ln)
            if m and cur:
                url = resolve(m.group(2), "README.md")
                if not url or not url.startswith(BASE):
                    continue
                cur[1].append((m.group(1), url, rewrite_line(m.group(3), "README.md")))
    return [g for g in groups if g[1]]


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: site-sync.py <site-root>")
    site = os.path.abspath(sys.argv[1])
    if not os.path.isdir(site):
        sys.exit("not a directory: " + site)
    out_docs = os.path.join(site, "rota", "docs")
    nav_path = os.path.join(site, "_data", "rota", "nav.yml")
    data_dir = os.path.dirname(nav_path)
    shutil.rmtree(out_docs, ignore_errors=True)
    for name in ("nav.yml", "skills.json", "chains.json"):
        if os.path.exists(os.path.join(data_dir, name)):
            os.remove(os.path.join(data_dir, name))
    os.makedirs(os.path.dirname(nav_path), exist_ok=True)
    for rel in MD:
        dst = os.path.join(out_docs, out_rel(rel))
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        with open(dst, "w", encoding="utf-8", newline="\n") as f:
            f.write(convert(rel))
    for rel in OTHER:
        dst = os.path.join(out_docs, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copyfile(os.path.join(DOCS, rel), dst)
    groups = parse_nav()
    simple = re.compile(r"^[A-Za-z0-9][A-Za-z0-9 ]*$")
    y = []
    for name, items in groups:
        y.append("- group: " + (name if simple.match(name) else q(name)))
        y.append("  items:")
        for t, u, d in items:
            y.append("    - title: " + (t if simple.match(t) else q(t)))
            y.append("      url: " + u)
            y.append("      desc: " + q(d))
    with open(nav_path, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(y) + "\n")
    skills, chains = skill_data()
    for name, val in (("skills.json", skills), ("chains.json", chains)):
        with open(os.path.join(data_dir, name), "w", encoding="utf-8", newline="\n") as f:
            json.dump(val, f, indent=1, ensure_ascii=False, sort_keys=True)
            f.write("\n")
    navurls = {u for _, items in groups for _, u, _ in items}
    for rel in MD:
        if permalink(rel) not in navurls:
            warn("page in no nav group: " + permalink(rel))
    print("%d pages, %d files copied, %d nav groups, %d skills, %d chains, %d warnings"
          % (len(MD), len(OTHER), len(groups), len(skills), len(chains), len(warnings)))


if __name__ == "__main__":
    main()

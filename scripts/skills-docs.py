#!/usr/bin/env python3
# Regenerates the skill lists in the other docs from docs/skills/*.md (one page
# per skill, plus the `chains:` front matter of docs/skills/README.md), so the
# lists live in one place. Python 3 stdlib only; front matter is read with
# scripts/fmlite.py.
#
# Each target file carries marked blocks:
#   <!-- generated:skills:<name> begin -->
#   <!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->
#   ...
#   <!-- generated:skills:<name> end -->
# Everything between the markers is rewritten; nothing else is touched.
#
# Usage: python3 scripts/skills-docs.py [--check] [--root <dir>]
#   (default)  rewrite the files in place and print which changed
#   --check    write nothing; exit 1 listing files that would change
#   --root     repo root to work on (default: the parent of this script's dir)
# Exit: 0 ok, 1 validation failure or (with --check) stale files.
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fmlite  # noqa: E402

LANES = [
    ('capture', 'Capture'),
    ('plan', 'Plan'),
    ('execute', 'Execute'),
    ('ship', 'Ship'),
    ('persist', 'Persist'),
    ('rounds', 'Rounds'),
    ('maintenance', 'Maintenance'),
]
LANE_NAME = dict(LANES)
REQUIRED = ['skill', 'lane', 'order', 'situation', 'summary', 'not_when',
            'before', 'after', 'manual_only', 'flags', 'example']
EDIT_NOTE = '<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->'


class Fail(Exception):
    pass


def read(path):
    with open(path, encoding='utf-8') as f:
        return f.read()


# -- loading and validation ----------------------------------------------------

def load(root):
    errs = []
    skills_dir = os.path.join(root, 'skills')
    pages_dir = os.path.join(root, 'docs', 'skills')
    dirs = sorted(d for d in os.listdir(skills_dir)
                  if d.startswith('rota-') and os.path.isdir(os.path.join(skills_dir, d)))
    pages = sorted(f[:-3] for f in os.listdir(pages_dir)
                   if f.startswith('rota-') and f.endswith('.md'))
    for d in dirs:
        if d not in pages:
            errs.append('skills/%s/ has no docs/skills/%s.md' % (d, d))
    for p in pages:
        if p not in dirs:
            errs.append('docs/skills/%s.md has no skills/%s/ directory' % (p, p))
    meta = {}
    for p in pages:
        path = os.path.join(pages_dir, p + '.md')
        rel = 'docs/skills/%s.md' % p
        try:
            fm, _ = fmlite.load_front_matter(path)
        except fmlite.FmError as e:
            errs.append(str(e))
            continue
        meta[p] = fm
        missing = [k for k in REQUIRED if k not in fm]
        for k in missing:
            errs.append('%s: missing required key %r' % (rel, k))
        if missing:
            continue
        if fm['skill'] != p:
            errs.append('%s: skill is %r, expected %r' % (rel, fm['skill'], p))
        if fm['lane'] not in LANE_NAME:
            errs.append('%s: lane %r is not one of %s' % (rel, fm['lane'], ', '.join(LANE_NAME)))
        if not isinstance(fm['order'], int) or isinstance(fm['order'], bool):
            errs.append('%s: order must be an integer' % rel)
        for k in ('situation', 'summary'):
            if not isinstance(fm[k], str) or not fm[k].strip():
                errs.append('%s: %s must be a non-empty string' % (rel, k))
        if not isinstance(fm['manual_only'], bool):
            errs.append('%s: manual_only must be true or false' % rel)
        for k in ('not_when', 'flags'):
            if not isinstance(fm[k], list):
                errs.append('%s: %s must be a list' % (rel, k))
        for k in ('before', 'after'):
            if not isinstance(fm[k], list):
                errs.append('%s: %s must be a list' % (rel, k))
        if isinstance(fm['not_when'], list):
            for n, it in enumerate(fm['not_when']):
                if not isinstance(it, dict) or 'use' not in it:
                    errs.append('%s: not_when[%d] needs a use key' % (rel, n))
                elif it['use'] not in pages:
                    errs.append('%s: not_when[%d].use references unknown skill %r' % (rel, n, it['use']))
        for k in ('before', 'after'):
            if isinstance(fm[k], list):
                for s in fm[k]:
                    if s not in pages:
                        errs.append('%s: %s references unknown skill %r' % (rel, k, s))
        if isinstance(fm['flags'], list):
            for n, it in enumerate(fm['flags']):
                if not isinstance(it, dict) or 'flag' not in it or 'does' not in it:
                    errs.append('%s: flags[%d] needs flag and does keys' % (rel, n))
        ex = fm['example']
        if not isinstance(ex, dict):
            errs.append('%s: example must be a mapping' % rel)
        else:
            for k in ('steps', 'changes', 'reviewed'):
                if k not in ex:
                    errs.append('%s: example is missing %r' % (rel, k))
            src = ex.get('source')
            if src:
                errs.extend(check_doc_path(root, src, '%s: example.source' % rel))
    # unique orders
    seen = {}
    for p, fm in meta.items():
        o = fm.get('order')
        if o in seen:
            errs.append('docs/skills/%s.md: order %s is also used by %s' % (p, o, seen[o]))
        else:
            seen[o] = p
    # chains
    chains = []
    idx = os.path.join(pages_dir, 'README.md')
    try:
        fm, _ = fmlite.load_front_matter(idx)
        chains = fm.get('chains')
        if not isinstance(chains, list) or not chains:
            errs.append('docs/skills/README.md: chains must be a non-empty list')
            chains = []
    except fmlite.FmError as e:
        errs.append(str(e))
    ids = set()
    for c in chains:
        where = 'docs/skills/README.md: chain %r' % (c.get('id') if isinstance(c, dict) else c)
        if not isinstance(c, dict):
            errs.append('docs/skills/README.md: chain entry is not a mapping')
            continue
        for k in ('id', 'title', 'when', 'walkthrough', 'steps'):
            if k not in c:
                errs.append('%s: missing %r' % (where, k))
        if c.get('id') in ids:
            errs.append('%s: duplicate id' % where)
        ids.add(c.get('id'))
        if c.get('walkthrough'):
            errs.extend(check_doc_path(root, c['walkthrough'], where + ' walkthrough'))
        if 'featured' in c and not isinstance(c['featured'], bool):
            errs.append('%s: featured must be true or false' % where)
        for n, st in enumerate(c.get('steps') or []):
            if not isinstance(st, dict) or 'skill' not in st or 'why' not in st:
                errs.append('%s: steps[%d] needs skill and why' % (where, n))
            elif st['skill'] not in pages:
                errs.append('%s: steps[%d] references unknown skill %r' % (where, n, st['skill']))
    if errs:
        raise Fail('\n'.join(errs))
    return meta, chains


def check_doc_path(root, ref, what):
    path = ref.split('#', 1)[0]
    if not os.path.isfile(os.path.join(root, 'docs', path)):
        return ['%s: docs/%s does not exist' % (what, path)]
    return []


# -- rendering -----------------------------------------------------------------

def ordered(meta):
    return sorted(meta.values(), key=lambda m: m['order'])


def by_lane(meta):
    for lane, name in LANES:
        items = [m for m in ordered(meta) if m['lane'] == lane]
        if items:
            yield name, items


def with_period(s):
    s = s.strip()
    return s if s.endswith(('.', '!', '?')) else s + '.'


def no_period(s):
    s = s.strip()
    return s[:-1] if s.endswith('.') else s


def link(m, prefix):
    return '[`/%s`](%s%s.md)' % (m['skill'], prefix, m['skill'])


def cell(s):
    return s.replace('|', '\\|')


def render_readme(meta, chains):
    groups = {}
    for m in ordered(meta):
        groups.setdefault(m['situation'], []).append(m)
    out = ['| When | Skill | What it does |', '|---|---|---|']
    for sit, items in sorted(groups.items(), key=lambda kv: kv[1][0]['order']):
        skills = ', '.join('`/%s`' % m['skill'] for m in items)
        what = '; '.join(cell(no_period(m['summary'])) for m in items)
        out.append('| %s | %s | %s |' % (cell(sit), skills, what))
    return out


def render_chains(meta, chains, root=None):
    out = []
    for c in chains:
        out.append('### ' + c['title'])
        out.append('')
        out.append(c['when'])
        out.append('')
        for n, st in enumerate(c['steps'], 1):
            out.append('%d. %s: %s' % (n, link(meta[st['skill']], 'skills/'), st['why']))
        if c.get('walkthrough'):
            out.append('')
            out.append('Walkthrough: [%s](%s)' % (c['_wt_title'], c['walkthrough']))
        out.append('')
    return out[:-1]


def render_index(meta, chains):
    out = []
    for name, items in by_lane(meta):
        out.append('## ' + name)
        out.append('')
        for m in items:
            line = '- %s: %s' % (link(m, ''), with_period(m['summary']))
            if m['manual_only']:
                line += ' Manual only.'
            flags = [f['flag'] for f in m['flags'] if not f['flag'].startswith('(')]
            if flags:
                line += ' Flags: %s.' % ', '.join('`%s`' % f for f in flags)
            out.append(line)
        out.append('')
    return out[:-1]


def render_nav(meta, chains):
    out = ['- [All skills](skills/README.md): when to use each skill, and how they chain']
    for m in ordered(meta):
        out.append('- %s: %s' % (link(m, 'skills/'), no_period(m['summary'])))
    return out


# -- block replacement -----------------------------------------------------------

def replace_block(text, name, body, rel):
    begin = '<!-- generated:skills:%s begin -->' % name
    end = '<!-- generated:skills:%s end -->' % name
    lines = text.split('\n')
    try:
        b = lines.index(begin)
    except ValueError:
        raise Fail('%s: missing marker line %s' % (rel, begin))
    try:
        e = lines.index(end, b + 1)
    except ValueError:
        raise Fail('%s: missing marker line %s' % (rel, end))
    new = lines[:b + 1] + [EDIT_NOTE, ''] + body + lines[e:]
    return '\n'.join(new)


TARGETS = [
    ('README.md', 'readme', render_readme),
    ('docs/how-it-works.md', 'chains', render_chains),
    ('docs/skills/README.md', 'index', render_index),
    ('docs/README.md', 'nav', render_nav),
]


def main(argv):
    check = False
    root = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), '..'))
    args = list(argv)
    while args:
        a = args.pop(0)
        if a == '--check':
            check = True
        elif a == '--root' and args:
            root = os.path.abspath(args.pop(0))
        else:
            print('usage: skills-docs.py [--check] [--root <dir>]', file=sys.stderr)
            return 2
    try:
        meta, chains = load(root)
        for c in chains:
            c['_wt_title'] = ''
            if c.get('walkthrough'):
                wt = read(os.path.join(root, 'docs', c['walkthrough'].split('#', 1)[0]))
                m = re.search(r'^# (.+)$', wt, re.M)
                if not m:
                    raise Fail('docs/%s: no # heading for the walkthrough title' % c['walkthrough'])
                c['_wt_title'] = m.group(1).strip()
        texts = {}
        for rel, name, fn in TARGETS:
            if rel not in texts:
                texts[rel] = read(os.path.join(root, rel))
            texts[rel] = replace_block(texts[rel], name, fn(meta, chains), rel)
    except Fail as e:
        print('skills-docs: %s' % e, file=sys.stderr)
        return 1
    changed = []
    for rel, new in texts.items():
        path = os.path.join(root, rel)
        if read(path) != new:
            changed.append(rel)
            if not check:
                with open(path, 'w', encoding='utf-8') as f:
                    f.write(new)
    if check:
        if changed:
            print('skills-docs: out of date, run python3 scripts/skills-docs.py:', file=sys.stderr)
            for rel in changed:
                print('  ' + rel, file=sys.stderr)
            return 1
        return 0
    for rel in changed:
        print('updated ' + rel)
    if not changed:
        print('skills-docs: no changes')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))

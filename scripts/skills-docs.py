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
from lanes import LANES  # noqa: E402
from skilldocs import Fail, load, read  # noqa: E402
EDIT_NOTE = '<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->'


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


def link(m, prefix):
    return '[`/%s`](%s%s.md)' % (m['skill'], prefix, m['skill'])


def render_chains(meta, chains):
    out = []
    for c in chains:
        out.append('### ' + c['title'])
        out.append('')
        out.append(c['when'])
        out.append('')
        for n, st in enumerate(c['steps'], 1):
            out.append('%d. %s: %s' % (n, link(meta[st['skill']], ''), st['why']))
        if c.get('walkthrough'):
            out.append('')
            out.append('Walkthrough: [%s](%s)' % (c['_wt_title'], '../' + c['walkthrough']))
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
    ('docs/skills/workflows.md', 'chains', render_chains),
    ('docs/skills/README.md', 'index', render_index),
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

# The one validated loader for docs/skills/*.md front matter and the chains in
# docs/skills/README.md, shared by skills-docs.py and site-sync.py so a valid
# skill page has a single definition. Python 3 stdlib only; front matter is
# read with scripts/fmlite.py.
import os

import fmlite
from lanes import lane_error

REQUIRED = ['skill', 'lane', 'order', 'situation', 'summary', 'not_when',
            'before', 'after', 'manual_only', 'flags', 'example']


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
        if lane_error(rel, fm['lane']):
            errs.append(lane_error(rel, fm['lane']))
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
        errs.extend(check_example(root, fm['example'], rel))
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
            if isinstance(st, dict) and 'example' in st:
                errs.extend(check_example(root, st['example'], '%s: steps[%d]' % (where, n)))
    if errs:
        raise Fail('\n'.join(errs))
    return meta, chains


def check_example(root, ex, where):
    # Shared by a skill's example and a chain step's example.
    if not isinstance(ex, dict):
        return ['%s: example must be a mapping' % where]
    errs = []
    for k in ('steps', 'changes', 'reviewed'):
        if k not in ex:
            errs.append('%s: example is missing %r' % (where, k))
    if 'reviewed' in ex and not isinstance(ex['reviewed'], bool):
        errs.append('%s: example.reviewed must be true or false' % where)
    src = ex.get('source')
    if src:
        errs.extend(check_doc_path(root, src, '%s: example.source' % where))
    return errs


def check_doc_path(root, ref, what):
    path = ref.split('#', 1)[0]
    if not os.path.isfile(os.path.join(root, 'docs', path)):
        return ['%s: docs/%s does not exist' % (what, path)]
    return []

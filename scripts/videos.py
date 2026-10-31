#!/usr/bin/env python3
# Parses, captures and drift-checks the video series under videos/. Python 3
# stdlib only (front matter is read with scripts/fmlite.py), so the drift check
# runs in test/doclint.sh and CI with no render stack: no Node, no Chrome, no
# TTS model. Rendering itself is videos/render.sh.
#
# Layout under the root (default: <repo>/videos):
#   episodes/NN-slug.md      the scripts (#2535): front matter, then shots
#   snapshots/NN-slug.json   committed capture of every terminal shot
#   out/manifest.json        written by the render: rota version + source hash
#
# Usage:
#   videos.py parse FILE                     print the parsed script as JSON
#   videos.py capture FILE [--write PATH]    run the terminal shots, print the snapshot
#   videos.py --check                        name every stale episode (exit 1)
# Options: --root DIR (videos dir), --bin PATH (rota binary; default $ROTA_BIN, else rota on PATH)
#
# Shot grammar: `### NN motion|terminal`, then `key: value` paragraphs (vo,
# scene, vertical, lower-third, crop, staged) and, for a terminal shot, one
# fenced `sh` block whose lines run in order in one scratch project.
# Exit: 0 ok / current, 1 stale or a script that does not parse, 2 usage.
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fmlite  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SHOT_RE = re.compile(r'^### (\d+) (motion|terminal)[ \t]*$')
KEY_RE = re.compile(r'^(vo|say|scene|vertical|lower-third|crop|staged):[ \t]*(.*)$')
FENCE_RE = re.compile(r'^```(\w*)[ \t]*$')
ANSI_RE = re.compile(r'\x1b\[[0-9;?]*[ -/]*[@-~]')
# The one build stamp the captures normalise: `rota 0.28.0 (7eaa16a, 2026-10-31T12:38:31Z)`.
# The version stays; the commit and build time change on every build and say nothing
# about whether a command, flag or output changed.
STAMP_RE = re.compile(r'\((?:[0-9a-f]{7,40}|unknown|dirty|none)(?:-dirty)?, [^)]*\)')
SCRATCH_PLACEHOLDER = '/tmp/scratch'
HOME_PLACEHOLDER = '/home/user'
CMD_TIMEOUT = 120
# Older drafts respelled the name "Ro-Ta" in vo:; captions always show "rota".
NAME_SPELLING_RE = re.compile(r'\bRo-Ta\b')


CROP_RE = re.compile(r'(?:(?P<who>[^;]*?)\s+keeps? (?:its |their |the )?|keep the )(?P<mode>first|last) (?P<n>\d+) lines?')


class VideoError(Exception):
    pass


def parse_script(path):
    """Parse one episode script. Returns {front, slug, shots: [...]}."""
    try:
        front, body = fmlite.load_front_matter(path)
    except fmlite.FmError as e:
        raise VideoError(str(e))
    shots = []
    cur = None
    lines = body.split('\n')
    i = 0
    while i < len(lines):
        line = lines[i]
        m = SHOT_RE.match(line)
        if m:
            cur = {'n': m.group(1), 'kind': m.group(2), 'vo': None, 'say': None, 'scene': None,
                   'vertical': None, 'lower_third': None, 'crop': None,
                   'staged': None, 'cmds': []}
            shots.append(cur)
            i += 1
            continue
        if cur is None:
            i += 1
            continue
        fm = FENCE_RE.match(line)
        if fm:
            j = i + 1
            block = []
            while j < len(lines) and not lines[j].startswith('```'):
                block.append(lines[j])
                j += 1
            if fm.group(1) == 'sh':
                if cur['kind'] != 'terminal':
                    raise VideoError('%s: shot %s is a motion shot with an sh block' % (path, cur['n']))
                for c in block:
                    c = c.strip()
                    if c and not c.startswith('#'):
                        cur['cmds'].append(c)
            i = j + 1
            continue
        km = KEY_RE.match(line)
        if km:
            key = km.group(1).replace('-', '_')
            text = [km.group(2)]
            j = i + 1
            while j < len(lines) and lines[j].strip() and not KEY_RE.match(lines[j]) \
                    and not SHOT_RE.match(lines[j]) and not FENCE_RE.match(lines[j]):
                text.append(lines[j])
                j += 1
            cur[key] = ' '.join(t.strip() for t in text).strip()
            i = j
            continue
        i += 1
    if not shots:
        raise VideoError('%s: no shots (### NN motion|terminal headings)' % path)
    for idx, s in enumerate(shots):
        if int(s['n']) != idx + 1:
            raise VideoError('%s: shot %s is out of order (expected %02d)' % (path, s['n'], idx + 1))
        if not s['vo']:
            raise VideoError('%s: shot %s has no vo: text' % (path, s['n']))
        if s['kind'] == 'terminal' and not s['cmds']:
            raise VideoError('%s: terminal shot %s has no sh block' % (path, s['n']))
        # vo: is the caption text; an optional say: is the spoken form when it differs.
        # The voice's lexicon turns "rota" into "Ro-Ta" (hard T), so scripts never respell it.
        s['caption'] = NAME_SPELLING_RE.sub('rota', s['vo'])
        s['speak'] = s['say'] or s['vo']
        s['crops'] = crop_rules(s['crop'], s['cmds'])
    slug = os.path.splitext(os.path.basename(path))[0]
    return {'slug': slug, 'front': front, 'shots': shots}


def crop_rules(crop, cmds):
    """Turn a shot's crop: prose into {cmd: {mode, n}} for the patterns scripts use:
    "<command> keeps its last N lines" (per command; one clause may name several, split
    by ';') and "keep the first N lines" (every command). Prose that does not match
    (for example "keep the FAIL line") crops nothing: bottom-anchoring scrolls it."""
    rules = {}
    for clause in (crop or '').split(';'):
        m = CROP_RE.search(clause)
        if not m:
            continue
        rule = {'mode': m.group('mode'), 'n': int(m.group('n'))}
        who = (m.group('who') or '').strip()
        for c in cmds:
            if not who or any(w.strip() and w.strip() in c for w in re.split(r'\band\b|,', who)):
                rules.setdefault(c, rule)
    return rules


def source_hash(path):
    with open(path, 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest()


def normalise(text, scratch, home):
    text = ANSI_RE.sub('', text)
    text = text.replace(scratch, SCRATCH_PLACEHOLDER).replace(home, HOME_PLACEHOLDER)
    text = STAMP_RE.sub('(build)', text)
    return text.replace('\r\n', '\n')


def resolve_bin(arg):
    b = arg or os.environ.get('ROTA_BIN') or shutil.which('rota')
    if not b or not os.path.exists(b):
        raise VideoError('no rota binary: pass --bin or set $ROTA_BIN')
    return os.path.abspath(b)


def capture(script, rota_bin, staged_dir=None, warn=False):
    """Run the terminal shots of a parsed script in a throwaway project.

    One scratch git project per episode; HOME, XDG_CONFIG_HOME, TZ and the git
    identity are pinned and every other variable (ROTA_*, GIT_*, HERDR, TMUX,
    tokens) is dropped, so a capture never touches the real repo, the user's
    config or a forge. Staged shots are not run: they read a committed fixture
    or carry none.
    """
    captures = []
    with tempfile.TemporaryDirectory(prefix='rota-video-') as td:
        td = os.path.realpath(td)
        scratch = os.path.join(td, 'project')
        home = os.path.join(td, 'home')
        xdg = os.path.join(td, 'xdg')
        shim = os.path.join(td, 'bin')
        for d in (scratch, home, xdg, shim):
            os.mkdir(d)
        os.symlink(rota_bin, os.path.join(shim, 'rota'))
        env = {
            'PATH': shim + os.pathsep + os.environ.get('PATH', '/usr/bin:/bin'),
            'HOME': home, 'XDG_CONFIG_HOME': xdg, 'TZ': 'UTC',
            'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TERM': 'dumb', 'NO_COLOR': '1',
            'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1',
            'GIT_AUTHOR_NAME': 'Ada', 'GIT_AUTHOR_EMAIL': 'ada@example.com',
            'GIT_COMMITTER_NAME': 'Ada', 'GIT_COMMITTER_EMAIL': 'ada@example.com',
            'GIT_AUTHOR_DATE': '2026-01-01T00:00:00Z', 'GIT_COMMITTER_DATE': '2026-01-01T00:00:00Z',
        }
        subprocess.run(['git', 'init', '-q', '-b', 'main', scratch], env=env, check=True)
        for s in script['shots']:
            if s['kind'] != 'terminal':
                continue
            if s['staged']:
                fixture = _staged_fixture(staged_dir, s['staged'])
                if warn and not fixture:
                    print('videos.py: shot %s is staged (%s) and has no fixture in %s; it shows no output'
                          % (s['n'], s['staged'], staged_dir or 'staged/'), file=sys.stderr)
                for cmd in s['cmds']:
                    captures.append({'shot': s['n'], 'cmd': cmd, 'stdout': fixture,
                                     'exit': 0, 'staged': s['staged']})
                continue
            for cmd in s['cmds']:
                try:
                    p = subprocess.run(['sh', '-c', cmd], cwd=scratch, env=env, stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=CMD_TIMEOUT)
                    out, code = p.stdout.decode('utf-8', 'replace'), p.returncode
                except subprocess.TimeoutExpired as e:
                    out, code = (e.stdout or b'').decode('utf-8', 'replace'), 124
                captures.append({'shot': s['n'], 'cmd': cmd, 'stdout': normalise(out, scratch, home), 'exit': code})
    return {'episode': script['slug'], 'captures': captures}


def _staged_fixture(staged_dir, name):
    if staged_dir:
        p = os.path.join(staged_dir, name + '.txt')
        if os.path.isfile(p):
            with open(p, encoding='utf-8') as f:
                return f.read()
    return ''


def check(root, rota_bin):
    """Return a list of 'slug: reason' lines for stale episodes."""
    stale = []
    ep_dir = os.path.join(root, 'episodes')
    manifest = {}
    mpath = os.path.join(root, 'out', 'manifest.json')
    if os.path.isfile(mpath):
        with open(mpath, encoding='utf-8') as f:
            manifest = json.load(f).get('episodes', {})
    files = sorted(f for f in os.listdir(ep_dir) if f.endswith('.md')) if os.path.isdir(ep_dir) else []
    for fn in files:
        path = os.path.join(ep_dir, fn)
        slug = fn[:-3]
        try:
            script = parse_script(path)
        except VideoError as e:
            stale.append('%s: script does not parse (%s)' % (slug, e))
            continue
        now = capture(script, rota_bin, os.path.join(root, 'staged'))
        reasons = []
        for c in now['captures']:
            if c['exit'] != 0:
                reasons.append('command failed: shot %s `%s` exited %d' % (c['shot'], c['cmd'], c['exit']))
        snap_path = os.path.join(root, 'snapshots', slug + '.json')
        rendered = slug in manifest
        if os.path.isfile(snap_path):
            with open(snap_path, encoding='utf-8') as f:
                snap = json.load(f)
            old = {(c['shot'], c['cmd']): c for c in snap.get('captures', [])
                   if not c.get('staged')}
            for c in now['captures']:
                if c.get('staged'):
                    continue
                prev = old.get((c['shot'], c['cmd']))
                if prev is None:
                    reasons.append('script changed: shot %s `%s` is not in the snapshot' % (c['shot'], c['cmd']))
                elif prev['stdout'] != c['stdout'] or prev['exit'] != c['exit']:
                    reasons.append('output changed: shot %s `%s`' % (c['shot'], c['cmd']))
        elif rendered:
            reasons.append('no snapshot for a rendered episode')
        if rendered and manifest[slug].get('source_hash') != source_hash(path):
            reasons.append('script edited since render')
        for r in reasons:
            stale.append('%s: %s' % (slug, r))
    unrendered = [f[:-3] for f in files if f[:-3] not in manifest]
    return stale, len(files), unrendered


def main(argv):
    args = list(argv)
    opts = {'root': os.path.join(REPO, 'videos'), 'bin': None, 'write': None}
    for flag in ('--root', '--bin', '--write'):
        if flag in args:
            k = args.index(flag)
            if k + 1 >= len(args):
                print('videos.py: %s needs a value' % flag, file=sys.stderr)
                return 2
            opts[flag[2:]] = args[k + 1]
            del args[k:k + 2]
    if args == ['--check']:
        try:
            stale, n, unrendered = check(opts['root'], resolve_bin(opts['bin']))
        except VideoError as e:
            print('videos.py: %s' % e, file=sys.stderr)
            return 1
        if stale:
            print('stale video episodes (re-render with videos/render.sh <episode>):')
            for s in stale:
                print('  ' + s)
            return 1
        print('videos: %d episode(s) current' % n)
        if unrendered:
            print('not rendered yet (not stale): ' + ', '.join(unrendered))
        return 0
    if len(args) == 2 and args[0] in ('parse', 'capture'):
        try:
            script = parse_script(args[1])
            if args[0] == 'capture':
                staged = os.path.join(opts['root'], 'staged')
                out = capture(script, resolve_bin(opts['bin']), staged, warn=True)
            else:
                out = script
                out['source_hash'] = source_hash(args[1])
        except VideoError as e:
            print('videos.py: %s' % e, file=sys.stderr)
            return 1
        text = json.dumps(out, indent=1, ensure_ascii=False) + '\n'
        if opts['write']:
            os.makedirs(os.path.dirname(os.path.abspath(opts['write'])), exist_ok=True)
            with open(opts['write'], 'w', encoding='utf-8') as f:
                f.write(text)
        else:
            sys.stdout.write(text)
        return 0
    print('usage: videos.py parse|capture FILE [--write PATH] | --check  [--root DIR] [--bin PATH]',
          file=sys.stderr)
    return 2


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))

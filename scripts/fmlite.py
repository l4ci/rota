#!/usr/bin/env python3
# fmlite: a small, strict YAML-subset reader for the front matter of the docs
# pages, so scripts need no PyYAML (CI has none). Import it:
#   from fmlite import load_front_matter, parse, FmError
#
# Subset: a top-level mapping; nested mappings by indentation; block lists
# ("- item", "- key: v" with following indented keys); flow lists of bare
# words ([a, b]); scalars bare or double-quoted (\" and \\ escapes only);
# true/false/null; ints; literal block scalars (|); folded block scalars (> and
# >-, the form test/validate-skills.py advises for a SKILL.md description:
# lines join with a space, a blank line is a newline; >- drops the final
# newline). Anything else raises FmError with file:line. No comments, tabs,
# anchors, single quotes, keep (+) or indent-indicator block scalars, or
# multi-document files.
import re
import sys


class FmError(Exception):
    pass


KEY_RE = re.compile(r'^([A-Za-z_][A-Za-z0-9_-]*):(?: +(.*))?$')
INT_RE = re.compile(r'^-?(0|[1-9][0-9]*)$')
BARE_BAD_WORDS = {'yes', 'no', 'on', 'off', 'y', 'n', '~', 'true', 'false', 'null'}
FLOW_ITEM_RE = re.compile(r'^[A-Za-z0-9_./#-]+$')


class _Parser:
    def __init__(self, path, lines, first_lineno):
        self.path = path
        self.lines = list(lines)
        self.first = first_lineno

    def err(self, i, msg):
        raise FmError('%s:%d: %s' % (self.path, self.first + i, msg))

    def indent_of(self, i):
        s = self.lines[i]
        return len(s) - len(s.lstrip(' '))

    def is_blank(self, i):
        return self.lines[i].strip() == ''

    def skip_blank(self, i):
        while i < len(self.lines) and self.is_blank(i):
            i += 1
        return i

    def check_line(self, i):
        s = self.lines[i]
        if '\t' in s[:len(s) - len(s.lstrip())]:
            self.err(i, 'tab indentation is not supported')
        if s.lstrip().startswith('#'):
            self.err(i, 'comments are not supported')

    # -- scalars ----------------------------------------------------------
    def scalar(self, i, s):
        s = s.strip()
        if s.startswith('"'):
            return self.quoted(i, s)
        if s.startswith('['):
            return self.flow_list(i, s)
        if s[:1] in "'&*!%@`{|>#":
            self.err(i, 'unsupported scalar syntax: %s' % s)
        if s.startswith('- ') or s == '-':
            self.err(i, 'unexpected list item here')
        if ': ' in s or s.endswith(':') or ' #' in s:
            self.err(i, 'unquoted scalar contains ": " or " #": %s' % s)
        if s == 'true':
            return True
        if s == 'false':
            return False
        if s == 'null':
            return None
        if s.lower() in BARE_BAD_WORDS:
            self.err(i, 'ambiguous bare word %r (quote it)' % s)
        if INT_RE.match(s):
            return int(s)
        if re.match(r'^[-+]?[0-9][0-9_.:eE+-]*$', s):
            self.err(i, 'ambiguous bare number/date %r (quote it)' % s)
        return s

    def quoted(self, i, s):
        out = []
        j = 1
        while j < len(s):
            c = s[j]
            if c == '\\':
                if j + 1 >= len(s) or s[j + 1] not in '"\\':
                    self.err(i, 'only \\" and \\\\ escapes are supported')
                out.append(s[j + 1])
                j += 2
                continue
            if c == '"':
                if s[j + 1:].strip():
                    self.err(i, 'text after closing quote')
                return ''.join(out)
            out.append(c)
            j += 1
        self.err(i, 'unterminated double-quoted string')

    def flow_list(self, i, s):
        if not s.endswith(']'):
            self.err(i, 'flow list must close on the same line')
        inner = s[1:-1].strip()
        if not inner:
            return []
        items = []
        for part in inner.split(','):
            part = part.strip()
            if not FLOW_ITEM_RE.match(part):
                self.err(i, 'flow list items must be bare words: %r' % part)
            items.append(part)
        return items

    # -- blocks -----------------------------------------------------------
    def block(self, i, indent):
        """Parse the block starting at line i (indent == indent). Returns (value, next_i)."""
        i = self.skip_blank(i)
        self.check_line(i)
        s = self.lines[i].strip()
        if s == '-' or s.startswith('- '):
            return self.list_block(i, indent)
        return self.map_block(i, indent)

    def literal(self, i, parent_indent):
        """Block scalar body after line i-1 whose key sits at parent_indent."""
        body = []
        while i < len(self.lines) and (self.is_blank(i) or self.indent_of(i) > parent_indent):
            if '\t' in self.lines[i]:
                self.err(i, 'tab in block scalar')
            body.append(self.lines[i])
            i += 1
        # trailing blank lines belong to the surrounding structure
        while body and body[-1].strip() == '':
            body.pop()
        nonblank = [b for b in body if b.strip()]
        if not nonblank:
            return '', i
        common = min(len(b) - len(b.lstrip(' ')) for b in nonblank)
        text = '\n'.join(b[common:] if b.strip() else '' for b in body) + '\n'
        return text, i

    def folded(self, i, parent_indent, strip):
        """Folded block scalar body, read like literal() then folded."""
        text, i = self.literal(i, parent_indent)
        out, para = [], []
        for ln in text.split('\n')[:-1] if text else []:
            if ln.strip():
                para.append(ln.strip())
            else:
                out.append(' '.join(para))
                para = []
        out.append(' '.join(para))
        folded = '\n'.join(out).strip('\n')
        return (folded if strip or not folded else folded + '\n'), i

    def map_block(self, i, indent):
        result = {}
        while True:
            i = self.skip_blank(i)
            if i >= len(self.lines):
                break
            self.check_line(i)
            cur = self.indent_of(i)
            if cur < indent:
                break
            if cur > indent:
                self.err(i, 'unexpected indentation')
            text = self.lines[i].strip()
            if text == '-' or text.startswith('- '):
                self.err(i, 'list item where a key was expected')
            m = KEY_RE.match(text)
            if not m:
                self.err(i, 'expected "key: value", got: %s' % text)
            key, rest = m.group(1), m.group(2)
            if key in result:
                self.err(i, 'duplicate key %r' % key)
            if rest is None or rest.strip() == '':
                j = self.skip_blank(i + 1)
                if j < len(self.lines) and self.indent_of(j) > indent:
                    val, i = self.block(j, self.indent_of(j))
                else:
                    val, i = None, i + 1
            elif rest.strip() == '|':
                val, i = self.literal(i + 1, indent)
            elif rest.strip() in ('>', '>-'):
                val, i = self.folded(i + 1, indent, rest.strip() == '>-')
            else:
                val, i = self.scalar(i, rest), i + 1
            result[key] = val
        return result, i

    def list_block(self, i, indent):
        result = []
        while True:
            i = self.skip_blank(i)
            if i >= len(self.lines):
                break
            self.check_line(i)
            cur = self.indent_of(i)
            if cur < indent:
                break
            if cur > indent:
                self.err(i, 'unexpected indentation')
            text = self.lines[i].strip()
            if not (text == '-' or text.startswith('- ')):
                break
            content = text[1:].lstrip(' ')
            if content == '':
                j = self.skip_blank(i + 1)
                if j < len(self.lines) and self.indent_of(j) > indent:
                    val, i = self.block(j, self.indent_of(j))
                else:
                    val, i = None, i + 1
                result.append(val)
                continue
            off = len(text) - len(content)  # width of "- " plus extra spaces
            if content == '|':
                val, i = self.literal(i + 1, indent)
                result.append(val)
                continue
            if content in ('>', '>-'):
                val, i = self.folded(i + 1, indent, content == '>-')
                result.append(val)
                continue
            if KEY_RE.match(content) and not content.startswith('"'):
                # list of maps: rewrite the line as the first key at indent+off
                self.lines[i] = ' ' * (indent + off) + content
                val, i = self.map_block(i, indent + off)
                result.append(val)
                continue
            result.append(self.scalar(i, content))
            i += 1
        return result, i


def parse(text, path='<string>'):
    """Parse a bare mapping document (no --- fences)."""
    return _parse_lines(text.split('\n'), path, 1)


def _parse_lines(lines, path, first):
    p = _Parser(path, lines, first)
    i = p.skip_blank(0)
    if i >= len(p.lines):
        return {}
    p.check_line(i)
    if p.indent_of(i) != 0:
        p.err(i, 'top level must start at column 0')
    val, j = p.block(i, 0)
    j = p.skip_blank(j)
    if j < len(p.lines):
        p.err(j, 'unexpected content')
    if not isinstance(val, dict):
        p.err(i, 'top level must be a mapping')
    return val


def load_front_matter(path, text=None):
    """Return (front_matter_dict, body_text) for a markdown file with --- fences."""
    if text is None:
        with open(path, encoding='utf-8') as f:
            text = f.read()
    lines = text.split('\n')
    if not lines or lines[0].rstrip() != '---':
        raise FmError('%s:1: file does not start with a --- front matter fence' % path)
    for k in range(1, len(lines)):
        if lines[k].rstrip() == '---':
            fm = _parse_lines(lines[1:k], path, 2)
            return fm, '\n'.join(lines[k + 1:])
    raise FmError('%s:1: front matter is not closed by a --- line' % path)


if __name__ == '__main__':
    import json
    for a in sys.argv[1:]:
        try:
            print(json.dumps(load_front_matter(a)[0], indent=1))
        except FmError as e:
            print(e, file=sys.stderr)
            sys.exit(1)

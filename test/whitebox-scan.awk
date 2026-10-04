# White-box scanner for test/sections/*.sh (#46, A2 census). Prints one
# "file:line: message" per violation and nothing when the files are clean.
#
# A white-box assertion looks at bin/ internals instead of calling a verb:
# helper files, SKILL.md or reference-doc greps. Such lines are allowed only
# inside an explicit block:
#
#   # white-box-begin: go-unit A5 #49     goes away with the A5 Go unit test
#   # white-box-begin: A9 #53 keep        goes away when A9 deletes bin/
#   # white-box-begin: A9 #53 doclint     prose lint; moves to a docs lint at A9
#   ...
#   # white-box-end
#
# Blocks do not nest and must close before the file ends. The format is
# documented in docs/design/5.0-smoke-whitebox.md. Comment lines are exempt.
function tagok(t,   p, n) {
  if (t ~ /^A9 #53 (keep|doclint)$/) return 1
  if (match(t, /^go-unit A[3-8] #[0-9]+$/)) {
    p = substr(t, 10, 1); n = substr(t, 13)
    return (p == 3 && n == 47) || (p == 4 && n == 48) || (p == 5 && n == 49) || \
           (p == 6 && n == 50) || (p == 7 && n == 51) || (p == 8 && n == 52)
  }
  return 0
}
FNR == 1 { if (open) bad(open_file, open_line, "white-box-begin never closed"); open = 0 }
{ file = FILENAME }
/^[[:space:]]*# white-box-begin:/ {
  tag = $0; sub(/^[[:space:]]*# white-box-begin: */, "", tag); sub(/[[:space:]]+$/, "", tag)
  if (open) bad(FILENAME, FNR, "white-box-begin inside the block opened at line " open_line)
  if (!tagok(tag)) bad(FILENAME, FNR, "bad white-box tag '" tag "'")
  open = 1; open_line = FNR; open_file = FILENAME; next
}
/^[[:space:]]*# white-box-end[[:space:]]*$/ {
  if (!open) bad(FILENAME, FNR, "white-box-end without a begin")
  open = 0; next
}
/^[[:space:]]*# white-box/ { bad(FILENAME, FNR, "malformed white-box marker (use white-box-begin: / white-box-end)"); next }
open || /^[[:space:]]*#/ { next }
/\$\{?BIN\}?([^A-Za-z0-9_]|$)/ || /\$\{?REPO\}?"?\/bin/ || /bin\/\*/ || \
/install_helpers/ || /SKILL\.md/ || /PYTHONPATH=/ || /git ls-(tree|files)/ || \
/(^|[^A-Za-z0-9_])BIN=|export +BIN([^A-Za-z0-9_]|$)|environ\["BIN"\]/ || \
/(^|[^A-Za-z0-9_])[A-Za-z_][A-Za-z0-9_]*=\(?"?\$\{?REPO\}?"?([ ;)]|$)/ || \
/\$\{?REPO\}?"?\/(rota-|references|docs|README|CHANGELOG|test\/validate)/ {
  bad(FILENAME, FNR, "white-box assertion outside a white-box block: " substr($0, 1, 90))
}
END { if (open) bad(open_file, open_line, "white-box-begin never closed") }
function bad(f, n, m) { printf "%s:%d: %s\n", f, n, m }

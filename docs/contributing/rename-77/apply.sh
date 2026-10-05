#!/usr/bin/env bash
# Apply the #77 rename (port-phase names -> domain names) to a checkout.
#
#   apply.sh <checkout-dir>
#
# Reads map.tsv and text-edits.tsv next to this script. Does git mv for files,
# renames identifiers (whole words), applies the three structural edits in
# STRUCTURE below, rewrites phase labels in comments, then gofmt, go build and
# go vet. Leaves everything uncommitted. Refuses a dirty tree. Any anchor that
# is missing or matches twice stops the run, so a drifted main fails loudly
# instead of half-applying. Frozen records are moved, never edited.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
target=${1:?usage: apply.sh <checkout-dir>}
cd "$target"
[ -z "$(git status --porcelain)" ] || { echo "apply.sh: $target has uncommitted changes" >&2; exit 1; }
map=$here/map.tsv
edits=$here/text-edits.tsv
die() { echo "apply.sh: $*" >&2; exit 1; }

# ---- 1. preflight ---------------------------------------------------------
while IFS=$'\t' read -r kind old new _; do
  case $kind in
    file)
      [ -f "$old" ] || die "missing source file $old"
      [ ! -e "$new" ] || die "target already exists: $new" ;;
    ident)
      grep -rqw --include='*.go' -- "$old" internal/cli cmd/rota || die "identifier $old not found (map is stale)"
      if grep -rqw --include='*.go' -- "$new" internal/cli cmd/rota; then die "new name $new already in use"; fi ;;
  esac
done < <(grep -v '^#' "$map")

# ---- 2. file moves ---------------------------------------------------------
while IFS=$'\t' read -r kind old new _; do
  [ "$kind" = file ] && git mv "$old" "$new"
done < <(grep -v '^#' "$map")

# ---- 3. structure: replace the two block builders with per-group registration
# STRUCTURE (the only non-rename code edits; behaviour and command order unchanged):
#   a. tree.go: a6Commands()/a4Commands() -> one registration line per group.
#   b. item.go: a4Commands() -> withReadOnly(groups...), the read-only marking.
#   c. debug.go: a6Commands() -> debugCommands() and spikeCommands().
edit() { # file, old text, new text; old must occur exactly once
  python3 - "$@" <<'PY'
import sys
f, old, new = sys.argv[1:4]
s = open(f).read()
if s.count(old) != 1:
    sys.exit("apply.sh: structure edit: %s: expected 1 match, got %d:\n%s" % (f, s.count(old), old))
open(f, "w").write(s.replace(old, new))
PY
}
edit internal/cli/tree.go '	root.Subs = append(root.Subs, a6Commands()...)
	root.Subs = append(root.Subs, a4Commands()...)
' '	root.Subs = append(root.Subs, docsCommands()...)
	root.Subs = append(root.Subs, proofCommands()...)
	root.Subs = append(root.Subs, milestoneCommands()...)
	root.Subs = append(root.Subs, debugCommands()...)
	root.Subs = append(root.Subs, spikeCommands()...)
	root.Subs = append(root.Subs, withReadOnly(a4ItemCommands(), a4bCommands(), a4cCommands(), a4dCommands())...)
'
edit internal/cli/item.go 'func a4Commands() []*Command {
	cmds := append(append(append(a4ItemCommands(), a4bCommands()...), a4cCommands()...), a4dCommands()...)
	a4MarkReadOnly(cmds, "")
	return cmds
}' '// withReadOnly joins the tracker groups and wraps their read-only verbs.
func withReadOnly(groups ...[]*Command) []*Command {
	var cmds []*Command
	for _, g := range groups {
		cmds = append(cmds, g...)
	}
	a4MarkReadOnly(cmds, "")
	return cmds
}'
edit internal/cli/debug.go 'func a6Commands() []*Command {
	return append(append(append(docsCommands(), proofCommands()...), milestoneCommands()...), []*Command{
' 'func debugCommands() []*Command {
	return []*Command{
'
edit internal/cli/debug.go '		}},
		{Name: "spike"' '		}},
	}
}

func spikeCommands() []*Command {
	return []*Command{
		{Name: "spike"'
edit internal/cli/debug.go '	}...)
}
' '	}
}
'

# ---- 4. identifiers: exact names first, then prefixes (tests) ------------------
perl -e '
  open M, "<", $ARGV[0] or die; my (%x, %p);
  while (<M>) { chomp; next if /^#/; my ($k,$o,$n) = split /\t/; $x{$o}=$n if $k eq "ident"; $p{$o}=$n if $k eq "prefix"; }
  my $xs = join "|", map quotemeta, sort { length $b <=> length $a } keys %x;
  my $ps = join "|", map quotemeta, sort { length $b <=> length $a } keys %p;
  my @files = glob("internal/cli/*.go cmd/rota/*.go");
  for my $f (@files) {
    local @ARGV = ($f); local $^I = ""; my $n = 0;
    while (<>) { s/\b($xs)\b/$x{$1}/g; s/\b($ps)(?=\w)/$p{$1}/g; print; }
  }' "$map"

# ---- 5. comment and label edits (each old text must occur exactly once) --------
perl -e '
  open E, "<", $ARGV[0] or die;
  while (<E>) {
    chomp; next if /^#/; my ($f,$o,$n) = split /\t/;
    $o =~ s/\\n/\n/g; $n =~ s/\\n/\n/g;
    open F, "<", $f or die "text edit: $f: $!"; local $/; my $s = <F>; close F;
    my $c = () = $s =~ /\Q$o\E/g;
    die "text edit: $f: expected 1 match, got $c: $o\n" unless $c == 1;
    $s =~ s/\Q$o\E/$n/; open F, ">", $f or die; print F $s; close F;
  }' "$edits"

# ---- 6. format, then leftovers, then build ---------------------------------------
gofmt -w internal/cli/*.go cmd/rota/*.go
left=$(grep -rnE '\b[aA][46][A-Za-z0-9_]*\b|[A-Za-z]+A4[A-Za-z]*' --include='*.go' internal/cli cmd/rota internal/artifact | grep -vE '\b[0-9a-f]{6,}\b' || true)
[ -z "$left" ] || { echo "$left"; die "phase names remain"; }
go build ./...
go vet ./internal/cli/... ./cmd/rota/... ./internal/artifact/...
echo "apply.sh: done. Uncommitted. Next: go test ./cmd/rota -run '^TestFrozen' and ./internal/cli, then git diff -M --stat"

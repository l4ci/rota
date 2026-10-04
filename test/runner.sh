#!/usr/bin/env bash
# Smoke test for the rota binary. Builds a throwaway .rota/ in a tmpdir, then
# sources every section under test/sections/ in alphabetical order. Each
# section runs in the shared $TMP cwd and may rely on cumulative state from
# earlier sections — order is load-bearing.
# Usage: bash test/runner.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
TESTDIR="$REPO/test"

# Which sections run. SECTION_LIST (newline-separated paths, so paths may hold
# spaces) narrows the run to those sections, in the order given: phase
# acceptance runs only the sections a phase owns. Unset runs
# them all. Checked before anything is set up, and loudly: a typo must never end
# in "All smoke tests passed." for sections that did not run. A relative entry
# is taken from the repo root; every entry must be an existing test/sections/*.sh
# file; a list with no entries (set but empty) is an error too.
SECTIONS=()
if [ "${SECTION_LIST+set}" = set ]; then
  SECTIONS_DIR="$(cd "$TESTDIR/sections" && pwd -P)"
  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    case "$entry" in /*) path="$entry" ;; *) path="$REPO/$entry" ;; esac
    if [ ! -f "$path" ]; then
      echo "runner: SECTION_LIST entry is not an existing file: $entry" >&2
      exit 2
    fi
    dir="$(cd "$(dirname "$path")" && pwd -P)"
    case "$path" in
      *.sh) ;;
      *) echo "runner: SECTION_LIST entry is not a .sh section: $entry" >&2; exit 2 ;;
    esac
    if [ "$dir" != "$SECTIONS_DIR" ]; then
      echo "runner: SECTION_LIST entry is not in test/sections/: $entry" >&2
      exit 2
    fi
    SECTIONS+=("$dir/$(basename "$path")")
  done <<<"$SECTION_LIST"
  if [ "${#SECTIONS[@]}" -eq 0 ]; then
    echo "runner: SECTION_LIST is set but names no section" >&2
    exit 2
  fi
else
  SECTIONS=("$TESTDIR/sections/"*.sh)
  if [ ! -f "${SECTIONS[0]}" ]; then
    echo "runner: no sections found under $TESTDIR/sections" >&2
    exit 2
  fi
fi

# macOS mktemp returns /var/folders/... but the underlying dir is /private/var/folders/... .
# Resolve to the physical path here so sections comparing against $TMP match `pwd -P` output
# from verbs like `rota repo umbrella` (which would otherwise mismatch on Darwin).
# Root every temp dir of this run under one base (#110). Sections and
# the helpers all call mktemp, and sections replace the EXIT trap (F38), so
# per-site cleanup cannot be relied on: TMPDIR rooting lets the runner remove
# everything in one rm -rf. Go and Python callers inherit it too.
RUN_TMP="$(cd "$(mktemp -d)" && pwd -P)"

# Fixture repos commit and merge. The identity comes from here so the run does
# not depend on the developer's (or CI's missing) global git config.
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export TMPDIR="$RUN_TMP"
TMP="$(cd "$(mktemp -d)" && pwd -P)"

# Black-box target (#46): sections call "$ROTA_BIN <group> <verb>". It defaults
# to the Go binary built once from this checkout, stamped with the VERSION
# file so version-drift checks see a matching install. Point ROTA_BIN at any
# other `rota` binary (absolute path: sections cd) to run the suite against it.
# The binary and the scratch dir for the poison stand-ins below live under
# $RUN_TMP, so the EXIT trap removes them with everything else.
ROTA_STAGE="$(mktemp -d)"
if [ -z "${ROTA_BIN:-}" ]; then
  ROTA_VERSION="$(tr -d '[:space:]' < "$REPO/VERSION")"
  (cd "$REPO" && go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$ROTA_VERSION" \
    -o "$ROTA_STAGE/rota" ./cmd/rota) || { echo "runner: go build ./cmd/rota failed" >&2; exit 2; }
  ROTA_BIN="$ROTA_STAGE/rota"
fi
export ROTA_BIN
trap 'rm -rf "$RUN_TMP"' EXIT

# Forge and host guard: no section may reach a real gh, glab, herdr or tmux.
# The round runs inside herdr, so a real herdr or tmux call could close live
# agents' panes. Poison stand-ins sit first on PATH for every section; a
# section that wants a fake puts it in front of them, as it already does. A poison call logs itself
# and exits 99, and any logged call fails the run after the leak guard. A
# section that resets PATH must start it with "$ROTA_POISON_BIN".
export ROTA_POISON_BIN="$ROTA_STAGE/poison"  # sections that reset PATH keep this first
ROTA_POISON_LOG="$ROTA_STAGE/poison.log"
mkdir -p "$ROTA_POISON_BIN" && : > "$ROTA_POISON_LOG"
for cli in gh glab herdr tmux codex; do
  printf '#!/bin/sh\necho "%s $*" >> "%s"\nexit 99\n' "$cli" "$ROTA_POISON_LOG" > "$ROTA_POISON_BIN/$cli"
  chmod +x "$ROTA_POISON_BIN/$cli"
done
export PATH="$ROTA_POISON_BIN:$PATH"
# Nor may a section see the developer's installed skills: round assign finds the
# worker contract under $CLAUDE_CONFIG_DIR/skills (else $HOME/.claude/skills), so
# an installed rota would make "contract missing" fixtures pass on one machine
# and fail on another. Point it at an empty dir.
export CLAUDE_CONFIG_DIR="$RUN_TMP/claude-config"
mkdir -p "$CLAUDE_CONFIG_DIR"
# Same for the Codex user root, $HOME/.agents/skills: doctor's skills check
# hashes it against the tree under test, so an install matching main made a
# skill-editing branch fail "dry round: doctor" (#26). HOME is safe to point
# at an empty dir for the sections: the go build above already ran with the
# real one, git identity comes from GIT_* above, and gh/glab/herdr/tmux/codex
# are poisoned. A section that needs a home sets HOME itself, as 83/94/98 do.
export HOME="$RUN_TMP/home"
mkdir -p "$HOME"
# Nor may a section inherit this shell's live host identity (pane, tab,
# socket): sections that need one set fake values themselves.
for v in $(compgen -e | grep -E '^(HERDR_|TMUX)'); do unset "$v"; done

# Leak guard: snapshot $REPO/CLAUDE.md and the dev tree's tracked .rota/
# content before any section runs. Under v4.1's partial-tracking model
# (.rota/ files committed to the repo), a section helper that walks up past
# $TMP can clobber real project state. The post-loop assertion below
# restores + fails. The check is explicit-at-end (not EXIT-trap-based)
# because sections follow the F38 local-trap convention and overwrite
# EXIT — see test/lib.sh.
REPO_CLAUDE="$REPO/CLAUDE.md"
REPO_CLAUDE_SNAP=""
if [ -f "$REPO_CLAUDE" ]; then
  REPO_CLAUDE_SNAP="$(mktemp)"
  cp "$REPO_CLAUDE" "$REPO_CLAUDE_SNAP"
fi
REPO_AGENTS="$REPO/AGENTS.md"
REPO_AGENTS_SNAP=""
if [ -f "$REPO_AGENTS" ]; then
  REPO_AGENTS_SNAP="$(mktemp)"
  cp "$REPO_AGENTS" "$REPO_AGENTS_SNAP"
fi
# Snapshot dev tree's tracked .rota/ content. We snap the whole subtree
# (excluding gitignored paths) so any leak surfaces as a diff at the end.
REPO_ROTA_SNAP=""
if [ -d "$REPO/.rota" ]; then
  REPO_ROTA_SNAP="$(mktemp -d)"
  # Use git ls-files to capture exactly what git tracks, preserving paths.
  (cd "$REPO" && git ls-files .rota/) | while IFS= read -r f; do
    mkdir -p "$REPO_ROTA_SNAP/$(dirname "$f")"
    cp "$REPO/$f" "$REPO_ROTA_SNAP/$f"
  done
fi

cd "$TMP"
mkdir -p .rota/bugs .rota/features .rota/tasks .rota/milestones

cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
EOF
cat > .rota/MILESTONES.md <<'EOF'
# Milestones

_(no vision yet — run `/rota-vision` to brainstorm milestones)_

## Active milestones

_(none active — set with `/rota-vision`)_

## Milestones
EOF
echo '{"bugs":0,"features":0,"tasks":0,"milestones":0}' > .rota/counters.json
echo '{"active":[]}' > .rota/status.json

git init -q
git config user.email t@t && git config user.name t
git checkout -q -b main 2>/dev/null || git branch -m main
git add -A && git commit -q -m "seed"

# Source pass()/fail() so sections can use them.
source "$TESTDIR/lib.sh"

# F62 — static preamble scan: shadowing / trap-convention violations
# in test/sections/*.sh fail fast before any section runs.
check_section_conventions "$TESTDIR/sections" || exit 1

# Source sections in alphabetical/numeric order. The numeric prefix is the
# canonical ordering; new sections insert at the next free slot.
#
# cd back to $TMP before each section. Under v4.1's partial-tracking model,
# a section that leaves cwd inside a sub-fixture (via inner cd) and lets the
# next section's `.rota/` writes target the dev tree's `.rota/` is a real leak.
# This pin is defensive — sections following the F38 local-trap convention
# should already restore cwd, but enforcing it at the boundary makes the
# leak guard catch only true walk-up clobbers, not cwd-drift residue.
#
# The loop runs in a subshell: sections replace the EXIT trap (F38), and the
# runner's own trap above must survive them to remove the temp tree.
# It is not written `( … ) || rc=$?`: bash ignores set -e inside a subshell
# that is the left side of `||`, so a failing section would carry on.
set +e
(
  set -e
  for f in "${SECTIONS[@]}"; do
    cd "$TMP"
    source "$f"
  done
)
SECTIONS_RC=$?
set -e

# Leak guard assertion: if any section wrote to $REPO/CLAUDE.md or any
# tracked .rota/ file in the dev tree, restore from snapshot and fail.
# Smoke is supposed to be hermetic w.r.t. $TMP; a diff here means a
# helper walked up past $TMP/.rota to the dev tree's.
LEAKED=0
if [ -n "$REPO_CLAUDE_SNAP" ] && ! cmp -s "$REPO_CLAUDE_SNAP" "$REPO_CLAUDE"; then
  printf '\n\033[31merror: smoke leaked into %s — restoring from snapshot\033[0m\n' "$REPO_CLAUDE" >&2
  cp "$REPO_CLAUDE_SNAP" "$REPO_CLAUDE"
  LEAKED=1
fi
if [ -n "$REPO_AGENTS_SNAP" ] && ! cmp -s "$REPO_AGENTS_SNAP" "$REPO_AGENTS"; then
  printf '\n\033[31merror: smoke leaked into %s — restoring from snapshot\033[0m\n' "$REPO_AGENTS" >&2
  cp "$REPO_AGENTS_SNAP" "$REPO_AGENTS"
  LEAKED=1
fi
if [ -n "$REPO_ROTA_SNAP" ]; then
  while IFS= read -r f; do
    snap_path="$REPO_ROTA_SNAP/$f"
    live_path="$REPO/$f"
    if [ -f "$snap_path" ] && [ -f "$live_path" ] && ! cmp -s "$snap_path" "$live_path"; then
      printf '\n\033[31merror: smoke leaked into %s — restoring from snapshot\033[0m\n' "$live_path" >&2
      cp "$snap_path" "$live_path"
      LEAKED=1
    elif [ -f "$snap_path" ] && [ ! -f "$live_path" ]; then
      printf '\n\033[31merror: smoke deleted %s — restoring from snapshot\033[0m\n' "$live_path" >&2
      mkdir -p "$(dirname "$live_path")"
      cp "$snap_path" "$live_path"
      LEAKED=1
    fi
  done < <(cd "$REPO_ROTA_SNAP" && find . -type f | sed 's|^\./||')
fi
[ -n "$REPO_CLAUDE_SNAP" ] && rm -f "$REPO_CLAUDE_SNAP"
[ -n "$REPO_AGENTS_SNAP" ] && rm -f "$REPO_AGENTS_SNAP"
[ -n "$REPO_ROTA_SNAP" ] && rm -rf "$REPO_ROTA_SNAP"
# Temp-dir guard (#110): everything the run made is under $RUN_TMP. Entries
# other than the runner's own were left behind by sections or helpers; report
# the count so growth shows up, then the EXIT trap removes it all.
RUN_LEFT="$(find "$RUN_TMP" -mindepth 1 -maxdepth 1 ! -path "$TMP" ! -path "$ROTA_STAGE" 2>/dev/null | wc -l | tr -d ' ')"
[ "$RUN_LEFT" -eq 0 ] || printf 'note: %s temp entries left under %s by sections; removing them\n' "$RUN_LEFT" "$RUN_TMP" >&2
if [ "$RUN_LEFT" -gt "${ROTA_SMOKE_TMP_MAX:-150}" ]; then
  printf '\n\033[31merror: %s temp entries left under %s (limit %s); a section or helper is leaking\033[0m\n' "$RUN_LEFT" "$RUN_TMP" "${ROTA_SMOKE_TMP_MAX:-150}" >&2
  LEAKED=1
fi
[ "$LEAKED" = 1 ] && exit 1
if [ -s "$ROTA_POISON_LOG" ]; then
  printf '\n\033[31merror: a section called a real forge or host CLI (poison gh/glab/herdr/tmux on PATH):\033[0m\n' >&2
  sed 's/^/  /' "$ROTA_POISON_LOG" >&2
  exit 1
fi
[ "$SECTIONS_RC" = 0 ] || exit "$SECTIONS_RC"
printf '\n\033[32mAll smoke tests passed.\033[0m\n'

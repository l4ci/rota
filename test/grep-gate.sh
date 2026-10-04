#!/usr/bin/env bash
# Final cutover gate for A9 (#53, S7). Run after bin/ is deleted, as the last
# check before the S7 PR leaves draft. Not part of smoke: it reads the tree,
# not a verb.
#
#   1. Nothing outside history and migration code uses the old name hv-skills,
#      or the old binary name hv (#236).
#   2. `rota init` in an empty git repo works and `rota init check` exits 0.
#
# The check for legacy 4.x helper names went with the rest of the hv-skills
# v3/v4 compatibility (#236).
# Usage: bash test/grep-gate.sh   (ROTA_BIN overrides the binary it builds)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"
fails=0
bad() { printf '\033[31mFAIL\033[0m %s\n' "$1" >&2; fails=$((fails + 1)); }

# 1. The old product name (#231). hv-skills became hv; what may still say
# hv-skills is history (CHANGELOG, the 5.0 design docs, tracked .rota/ state),
# code that has to know the old name (migrate hv, legacy-format fixtures) and
# text captured from real panes. The managed block markers keep the key
# "skills" (hv-skills-start/-end), so the pattern lets those through.
OLD_NAME='hv-skills(?!-(?:start|end)\b)'
OLD_SCOPE=(
  ':(exclude)CHANGELOG.md'
  ':(exclude)docs/design/'
  ':(exclude).rota/'
  ':(exclude)test/grep-gate.sh'
  # The "Coming from hv-skills" migration section names the predecessor.
  ':(exclude)docs/install.md'
  ':(exclude)internal/migrate/'
  # Legacy-format fixtures: a pre-rename block heading or .gitignore header.
  ':(exclude)internal/knowledge/knowledge_test.go'
  ':(exclude)internal/initproj/init_test.go'
  ':(exclude)test/sections/04_skills.sh'
  # Scrollback captured from real panes, where the checkout path shows.
  ':(exclude)internal/host/testdata/'
  ':(exclude)internal/worker/testdata/'
)
HITS="$(git grep -inIP "$OLD_NAME" -- . "${OLD_SCOPE[@]}" || true)"
if [ -n "$HITS" ]; then
  bad "the old product name hv-skills is still used ($(wc -l <<<"$HITS" | tr -d ' ') lines):"
  printf '%s\n' "$HITS" >&2
fi

# 1b. The old binary name (#236). hv became rota: skills /rota-*, state .rota/,
# env ROTA_*, markers <!-- rota:... --> and rota-<key> blocks, module
# github.com/l4ci/rota. What may still say hv: history, this repo's own state
# and instructions until the migration dogfoods them, the legacy read side
# (old markers, blocks and stamps are read until a project is migrated), the
# migrate hv, and the files kit's and lea's
# slices of #236 rewrite next (front-door docs; release, install and update).
# Drop those slices' exclusions once they merge.
SKILLS='brainstorm|capture|debug|decide|learn|orchestrate|pause|plan|qa|refactor|release|review|ship|spike|vision|work'
HV_NAME="(?<![\\w.-])hv-(?:${SKILLS})(?![\\w-])|\\.hv/(?!bin\\b)|\\bHV_[A-Z]|\\bHV-(?:DONE|BLOCKED)\\b"
HV_NAME+="|(?:<|\\\\u003c)!-- hv[:-]|github\\.com/l4ci/hv\\b|\`hv[ \`]|\"hv\"|\\bhv\\.version\\b"
HV_SCOPE=(
  ':(exclude)CHANGELOG.md'
  ':(exclude)docs/design/5.0-helper-triage.md'
  ':(exclude)docs/design/5.0-smoke-whitebox.md'
  ':(exclude)docs/design/5.0-verb-contract.md'
  ':(exclude).rota/'
  ':(exclude)AGENTS.md'
  ':(exclude)CLAUDE.md'
  ':(exclude).gitignore'
  ':(exclude)test/grep-gate.sh'
  # Legacy read side and stamp migration, with their tests and fixtures.
  ':(exclude)internal/marker/'
  ':(exclude)internal/section/'
  ':(exclude)internal/round/move.go'
  ':(exclude)internal/cli/migrate_hv.go'
  ':(exclude)internal/cli/migrate_hv_test.go'
  ':(exclude)test/sections/98_migrate_hv.sh'
  ':(exclude)internal/round/move_test.go'
  ':(exclude)internal/config/fill.go'
  ':(exclude)internal/config/fill_test.go'
  ':(exclude)internal/cli/init_test.go'
  ':(exclude)internal/cli/init_umbrella_test.go'
  ':(exclude)test/sections/76_config_fill.sh'
  ':(exclude)test/sections/10_update.sh'
  ':(exclude)internal/config/schema.go'
  ':(exclude)internal/backlog/legacy_marker_test.go'
  ':(exclude)internal/backlog/issue_write_python_test.go'
  ':(exclude)internal/cli/glossary_test.go'
  ':(exclude)internal/cli/testdata/golden/TestInstructionsInitMatchGolden__*'
  ':(exclude)test/sections/02_knowledge.sh'
  ':(exclude)test/sections/13_helpers.sh'
  ':(exclude)test/sections/66_agents_md.sh'
  ':(exclude)internal/migrate/'
  # The "Coming from hv-skills" migration section names .hv/ and /hv-*.
  ':(exclude)docs/install.md'
)
HITS="$(git grep -nIP "$HV_NAME" -- . "${HV_SCOPE[@]}" || true)"
if [ -n "$HITS" ]; then
  bad "the old binary name hv is still used ($(wc -l <<<"$HITS" | tr -d ' ') lines):"
  printf '%s\n' "$HITS" >&2
fi

# 2. A fresh project initializes and passes its own check.
SCRATCH="$(mktemp -d)"
trap 'rm -rf "${SCRATCH:?}"' EXIT
if [ -z "${ROTA_BIN:-}" ]; then
  VERSION="$(tr -d '[:space:]' < VERSION)"
  go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$VERSION" -o "$SCRATCH/rota" ./cmd/rota
  ROTA_BIN="$SCRATCH/rota"
fi
mkdir "$SCRATCH/proj"
git -C "$SCRATCH/proj" init -q
if (cd "$SCRATCH/proj" && "$ROTA_BIN" init >/dev/null); then
  (cd "$SCRATCH/proj" && "$ROTA_BIN" init check >/dev/null) || bad "rota init check exits non-zero in a freshly initialized repo"
else
  bad "rota init failed in an empty git repo"
fi

[ "$fails" = 0 ] || { printf '%s check(s) failed\n' "$fails" >&2; exit 1; }
printf '\033[32mgrep gate passed.\033[0m\n'

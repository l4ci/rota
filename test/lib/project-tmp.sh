# The project temp root for the test harness (#2499), sourced by test/gate.sh.
# It mirrors rotatree.TempRoot: <main checkout>/.rota/tmp of the repository
# that holds <dir>, so a gate run from a scratch worktree keeps its logs beside
# the orchestrator's. A bare repository, a checkout without .rota/, or a
# symlinked .rota/ or .rota/tmp has no project root.

# project_tmp <dir>: print the project temp root (created 0700 with its
# self-ignoring .gitignore) and succeed, or print nothing and fail.
project_tmp() {
  local common main root
  common="$(git -C "$1" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || return 1
  common="$(cd "$common" 2>/dev/null && pwd -P)" || return 1
  [ "$(basename "$common")" = .git ] || return 1
  main="$(dirname "$common")"
  [ -d "$main/.rota" ] && [ ! -L "$main/.rota" ] || return 1
  root="$main/.rota/tmp"
  [ ! -L "$root" ] || return 1
  if [ ! -d "$root" ]; then
    (umask 077 && mkdir -p "$root") || return 1
  fi
  # An existing root must be ours and goes back to 0700, as ensureTempRoot does.
  [ -O "$root" ] && chmod 700 "$root" || return 1
  [ -e "$root/.gitignore" ] || [ -L "$root/.gitignore" ] || printf '*\n' > "$root/.gitignore" || return 1
  printf '%s\n' "$root"
}

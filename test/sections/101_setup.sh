echo "setup — rota setup (#25): --list, no-terminal refusal, --yes --set, second run"

# A fixture directory per case; stdin is /dev/null so setup never sees a terminal.
SU="$(mktemp -d)"
trap 'rm -rf "$SU"' EXIT
su() { # su <dir> <args...>: run rota setup in <dir>
  local d="$1"; shift
  ( cd "$d" && "$ROTA_BIN" setup "$@" ) < /dev/null
}

echo "  --list writes nothing"
mkdir -p "$SU/list"
RC=0; su "$SU/list" --list >/dev/null 2>&1 || RC=$?
[ $RC -eq 0 ] || fail "setup --list should exit 0, got $RC"
[ -z "$(ls -A "$SU/list")" ] || fail "setup --list wrote into the directory: $(ls -A "$SU/list")"

echo "  no terminal and no answers: exit 2, nothing written"
mkdir -p "$SU/noans"
RC=0; su "$SU/noans" >/dev/null 2>&1 || RC=$?
[ $RC -eq 2 ] || fail "setup with no answers and no terminal should exit 2, got $RC"
[ -z "$(ls -A "$SU/noans")" ] || fail "refused setup still wrote: $(ls -A "$SU/noans")"

echo "  --yes --set writes the values"
mkdir -p "$SU/set"
( cd "$SU/set" && git init -q )
RC=0; su "$SU/set" --yes --set work.isolation=worktree --set work.mergeStrategy=pr >/dev/null 2>&1 || RC=$?
[ $RC -eq 0 ] || fail "setup --yes --set should exit 0, got $RC"
[ -d "$SU/set/.rota" ] || fail "setup --yes did not create .rota/"
GOT="$(cd "$SU/set" && "$ROTA_BIN" config show work.isolation 2>&1)" || vfail
case "$GOT" in *worktree*) ;; *) fail "work.isolation not set to worktree: $GOT" ;; esac
GOT="$(cd "$SU/set" && "$ROTA_BIN" config show work.mergeStrategy 2>&1)" || vfail
case "$GOT" in *pr*) ;; *) fail "work.mergeStrategy not set to pr: $GOT" ;; esac

echo "  second run on an initialized project exits 4"
RC=0; su "$SU/set" --yes >/dev/null 2>&1 || RC=$?
[ $RC -eq 4 ] || fail "setup on an initialized project should exit 4, got $RC"

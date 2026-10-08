echo "install.sh: signature- and checksum-verified install from a local fake release (F6b, #230, #2)"
command -v minisign >/dev/null 2>&1 || fail "section 97 needs minisign on PATH to sign the fake releases"
# No network: ROTA_RELEASE_BASE_URL points at file:// trees built here.
IS="$(mktemp -d)"
trap 'rm -rf "${IS:?}"' EXIT
# A throwaway key signs the fake releases; the script under test is a copy with
# that key swapped in for the embedded release key (there is no env override).
minisign -G -W -p "$IS/t.pub" -s "$IS/t.key" >/dev/null 2>&1 || fail "could not generate a test minisign key"
minisign -G -W -p "$IS/o.pub" -s "$IS/o.key" >/dev/null 2>&1 || fail "could not generate a second test key"
TESTPUB=$(tail -n1 "$IS/t.pub")
grep -q '^ROTA_MINISIGN_PUBKEY=' "$REPO/install.sh" || fail "install.sh has no embedded ROTA_MINISIGN_PUBKEY"
sed "s|^ROTA_MINISIGN_PUBKEY=.*|ROTA_MINISIGN_PUBKEY=$TESTPUB|" "$REPO/install.sh" > "$IS/install.sh"
INSTALL="$IS/install.sh"
case $(uname -s) in Linux) ios=linux ;; *) ios=darwin ;; esac
case $(uname -m) in x86_64 | amd64) iarch=amd64 ;; *) iarch=arm64 ;; esac
ASSET="rota_${ios}_${iarch}"
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# mkrel DIR LABEL: a release dir holding a fake rota, its signature and a matching checksums.txt.
mkrel() {
  mkdir -p "$1"
  printf '#!/bin/sh\necho "rota fake %s"\n' "$2" > "$1/$ASSET"
  minisign -S -s "$IS/t.key" -m "$1/$ASSET" -x "$1/$ASSET.minisig" >/dev/null 2>&1
  printf '%s  %s\n%s  rota_other_arch\n' "$(sha "$1/$ASSET")" "$ASSET" "0000" > "$1/checksums.txt"
}
mkrel "$IS/rel/latest/download" latest
mkrel "$IS/rel/download/v1.2.3" 1.2.3
export ROTA_RELEASE_BASE_URL="file://$IS/rel"

# Latest release into a fresh prefix: installs, runs, prints the next step.
OUT=$(sh "$INSTALL" --prefix "$IS/p1" 2>&1) || fail "latest install failed: $OUT"
[ -x "$IS/p1/bin/rota" ] || fail "latest install left no executable rota"
[ "$("$IS/p1/bin/rota")" = "rota fake latest" ] || fail "installed rota is not the latest asset"
case $OUT in *"rota skills install"*) ;; *) fail "install.sh should print the next step: $OUT" ;; esac
case $OUT in *"is not on your PATH"*) ;; *) fail "install.sh should say the prefix is off PATH: $OUT" ;; esac
[ -z "$(ls -A "$IS/p1/bin" | grep -v '^rota$' || true)" ] || fail "install left temp files in the bin dir"

# Pinned version, via the env and via a leading v; PATH already holding bin: no PATH hint.
OUT=$(PATH="$IS/p2/bin:$PATH" ROTA_PREFIX="$IS/p2" ROTA_VERSION=v1.2.3 sh "$INSTALL" 2>&1) || fail "pinned install failed: $OUT"
[ "$("$IS/p2/bin/rota")" = "rota fake 1.2.3" ] || fail "pinned install is not v1.2.3"
case $OUT in *"is not on your PATH"*) fail "install.sh warned about PATH though bin is on it" ;; esac

# Signature failures: tampered binary, wrong key, missing .minisig, no minisign. All fail
# closed before anything is installed, and an existing rota is untouched.
sigfail() { # sigfail NAME WANT-TEXT [env VAR=val ...]: install from $IS/NAME must fail with WANT-TEXT
  name=$1; want=$2; shift 2
  mkdir -p "$IS/q-$name/bin"; printf 'old\n' > "$IS/q-$name/bin/rota"
  RC=0; OUT=$(env "$@" ROTA_RELEASE_BASE_URL="file://$IS/$name" sh "$INSTALL" --prefix "$IS/q-$name" --version 7.7.7 2>&1) || RC=$?
  [ "$RC" != 0 ] || fail "$name: install should fail"
  case $OUT in *"$want"*) ;; *) fail "$name: should say '$want': $OUT" ;; esac
  [ "$(cat "$IS/q-$name/bin/rota")" = "old" ] || fail "$name: a failed install replaced the existing rota"
  [ -z "$(ls -A "$IS/q-$name/bin" | grep -v '^rota$' || true)" ] || fail "$name: a failed install left temp files"
}
# Tampered after signing, with checksums.txt rewritten to match (the attack sha256 alone cannot see).
mkrel "$IS/sigtamper/download/v7.7.7" 7.7.7
printf 'tampered\n' >> "$IS/sigtamper/download/v7.7.7/$ASSET"
printf '%s  %s\n' "$(sha "$IS/sigtamper/download/v7.7.7/$ASSET")" "$ASSET" > "$IS/sigtamper/download/v7.7.7/checksums.txt"
sigfail sigtamper "signature check failed" PATH="$PATH"
# Validly signed, but by a key that is not the release key.
mkrel "$IS/sigother/download/v7.7.7" 7.7.7
minisign -S -s "$IS/o.key" -m "$IS/sigother/download/v7.7.7/$ASSET" -x "$IS/sigother/download/v7.7.7/$ASSET.minisig" >/dev/null 2>&1
sigfail sigother "signature check failed" PATH="$PATH"
# No .minisig published.
mkrel "$IS/signone/download/v7.7.7" 7.7.7
rm -f "$IS/signone/download/v7.7.7/$ASSET.minisig"
sigfail signone "download failed" PATH="$PATH"
# minisign not installed: cannot verify, so refuse (PATH holds only the basics, no minisign).
mkrel "$IS/sigtool/download/v7.7.7" 7.7.7
mkdir -p "$IS/nomini"
for t in sh awk cut mkdir mktemp rm mv chmod cat uname curl sha256sum shasum dirname printf; do
  tp=$(command -v "$t" 2>/dev/null || true); [ -z "$tp" ] || [ "${tp#/}" = "$tp" ] || ln -sf "$tp" "$IS/nomini/$t"
done
sigfail sigtool "need minisign" PATH="$IS/nomini" ROTA_STRICT=1
# --strict flag behaves the same as ROTA_STRICT=1.
mkdir -p "$IS/q-flag/bin"
RC=0; OUT=$(PATH="$IS/nomini" ROTA_RELEASE_BASE_URL="file://$IS/sigtool" sh "$INSTALL" --strict --prefix "$IS/q-flag" --version 7.7.7 2>&1) || RC=$?
[ "$RC" != 0 ] || fail "--strict without minisign should fail"
case $OUT in *"need minisign"*) ;; *) fail "--strict should say 'need minisign': $OUT" ;; esac
[ ! -e "$IS/q-flag/bin/rota" ] || fail "--strict without minisign still installed"

# Without minisign and not strict: sha256 verified, loud warning, install completes.
RC=0; OUT=$(PATH="$IS/nomini" ROTA_RELEASE_BASE_URL="file://$IS/sigtool" sh "$INSTALL" --prefix "$IS/pnm" --version 7.7.7 2>&1) || RC=$?
[ "$RC" = 0 ] || fail "install without minisign should complete: $OUT"
case $OUT in *"signature was NOT checked"*) ;; *) fail "should warn the signature was not checked: $OUT" ;; esac
case $OUT in *"install minisign"*) ;; *) fail "should say how to install minisign: $OUT" ;; esac
[ "$("$IS/pnm/bin/rota")" = "rota fake 7.7.7" ] || fail "install without minisign did not install the asset"
# ROTA_STRICT=0 is not strict.
RC=0; OUT=$(PATH="$IS/nomini" ROTA_STRICT=0 ROTA_RELEASE_BASE_URL="file://$IS/sigtool" sh "$INSTALL" --prefix "$IS/pnm0" --version 7.7.7 2>&1) || RC=$?
[ "$RC" = 0 ] || fail "ROTA_STRICT=0 without minisign should complete: $OUT"
# The sha256 check still holds without minisign: a mismatch aborts, nothing installed.
mkrel "$IS/nmbad/download/v7.7.7" 7.7.7
printf 'tampered\n' >> "$IS/nmbad/download/v7.7.7/$ASSET"
RC=0; OUT=$(PATH="$IS/nomini" ROTA_RELEASE_BASE_URL="file://$IS/nmbad" sh "$INSTALL" --prefix "$IS/pnmbad" --version 7.7.7 2>&1) || RC=$?
[ "$RC" != 0 ] || fail "checksum mismatch without minisign should fail"
case $OUT in *"checksum mismatch"*) ;; *) fail "mismatch without minisign should say so: $OUT" ;; esac
[ ! -e "$IS/pnmbad/bin/rota" ] || fail "mismatch without minisign still installed"
# With minisign on PATH there is no warning.
OUT=$(sh "$INSTALL" --prefix "$IS/pwm" 2>&1) || fail "install with minisign failed: $OUT"
case $OUT in *"NOT checked"*) fail "install with minisign should not warn: $OUT" ;; esac

# Checksum mismatch: fails closed, nothing installed, an existing rota is untouched.
mkrel "$IS/bad/download/v9.9.9" 9.9.9
printf 'tampered\n' >> "$IS/bad/download/v9.9.9/$ASSET"
minisign -S -s "$IS/t.key" -m "$IS/bad/download/v9.9.9/$ASSET" -x "$IS/bad/download/v9.9.9/$ASSET.minisig" >/dev/null 2>&1  # signed, so only the sha256 check can object
mkdir -p "$IS/p3/bin"; printf 'old\n' > "$IS/p3/bin/rota"
RC=0; OUT=$(ROTA_RELEASE_BASE_URL="file://$IS/bad" sh "$INSTALL" --prefix "$IS/p3" --version 9.9.9 2>&1) || RC=$?
[ "$RC" != 0 ] || fail "a checksum mismatch should fail"
case $OUT in *"checksum mismatch"*) ;; *) fail "mismatch should say so: $OUT" ;; esac
[ "$(cat "$IS/p3/bin/rota")" = "old" ] || fail "a failed install replaced the existing rota"
[ -z "$(ls -A "$IS/p3/bin" | grep -v '^rota$' || true)" ] || fail "a failed install left temp files"

# No checksums entry for this platform: fails closed too.
mkrel "$IS/noent/download/v1.0.0" 1.0.0
printf '0000  rota_other_arch\n' > "$IS/noent/download/v1.0.0/checksums.txt"
RC=0; ROTA_RELEASE_BASE_URL="file://$IS/noent" sh "$INSTALL" --prefix "$IS/p4" --version 1.0.0 >/dev/null 2>&1 || RC=$?
[ "$RC" != 0 ] || fail "a checksums.txt without this asset should fail"
[ ! -e "$IS/p4/bin/rota" ] || fail "an install with no checksum entry still wrote rota"

# Missing release: fails, installs nothing.
RC=0; sh "$INSTALL" --prefix "$IS/p5" --version 0.0.1 >/dev/null 2>&1 || RC=$?
[ "$RC" != 0 ] && [ ! -e "$IS/p5/bin/rota" ] || fail "an unpublished version should fail without installing"

# URL guard: userinfo and remote http are refused before any download.
for bad in "https://x:y@github.com/l4ci/rota/releases" "http://evil.example/releases" "http://localhost:1@evil.example/r" "http://127.0.0.1:8000/releases"; do
  RC=0; OUT=$(ROTA_RELEASE_BASE_URL="$bad" sh "$INSTALL" --prefix "$IS/p6" 2>&1) || RC=$?
  [ "$RC" != 0 ] || fail "install.sh accepted release URL $bad"
  case $OUT in *"refusing release URL"*) ;; *) fail "$bad should be refused by name: $OUT" ;; esac
done
[ ! -e "$IS/p6" ] || fail "a refused URL still created the prefix"

# curl | sh: a script cut short runs nothing. Cut it at every line of main()'s
# body and just before the closing call; each cut must leave no rota behind, and
# HOME is a scratch dir, so a cut that did run would show up there.
n=$(wc -l < "$INSTALL")
for cut in 30 45 60 80 100 $((n - 2)) $((n - 1)); do
  head -n "$cut" "$INSTALL" > "$IS/cut.sh"
  rm -rf "$IS/home"; mkdir -p "$IS/home"
  HOME="$IS/home" sh < "$IS/cut.sh" >/dev/null 2>&1 || true
  [ -z "$(ls -A "$IS/home")" ] || fail "a script cut at line $cut touched HOME: $(ls -A "$IS/home")"
done
# The last line is the only call: drop it and nothing runs, even with env set that would install.
head -n "$((n - 1))" "$INSTALL" > "$IS/cut.sh"
OUT=$(ROTA_PREFIX="$IS/pcut" sh < "$IS/cut.sh" 2>&1) || fail "a copy without its last line should exit 0 quietly: $OUT"
[ -z "$OUT" ] && [ ! -e "$IS/pcut" ] || fail "a copy without its last line did something: $OUT"
# --help reads no file: it works with the script on stdin ($0 is just sh).
OUT=$(sh -s -- --help < "$INSTALL" 2>&1) || fail "--help over stdin failed: $OUT"
case $OUT in *"Usage: install.sh"*) ;; *) fail "--help should print usage: $OUT" ;; esac
[ ! -e "$IS/phelp" ] || fail "--help touched the filesystem"

# A signal mid-run leaves no temp dir behind.
mkdir -p "$IS/sigrel/latest/download"
printf '#!/bin/sh\n' > "$IS/sigrel/latest/download/$ASSET"
sleepbin="$IS/fakebin"; mkdir -p "$sleepbin"
printf '#!/bin/sh\necho $$ > "$IS_CURL_PID"\nexec sleep 30\n' > "$sleepbin/curl"; chmod +x "$sleepbin/curl"
IS_CURL_PID="$IS/curl.pid" PATH="$sleepbin:$PATH" ROTA_RELEASE_BASE_URL="file://$IS/sigrel" sh "$INSTALL" --prefix "$IS/psig" >/dev/null 2>&1 &
SIGPID=$!
i=0; while [ ! -d "$IS/psig/bin" ] || [ -z "$(ls -A "$IS/psig/bin" 2>/dev/null)" ]; do i=$((i + 1)); [ "$i" -lt 100 ] || break; sleep 0.1; done
# A terminal's ^C reaches the whole foreground group: sh defers its trap until the
# running curl exits, so stop the fake curl with it instead of waiting out its sleep.
kill -TERM "$SIGPID" "$(cat "$IS/curl.pid" 2>/dev/null)" 2>/dev/null || true
wait "$SIGPID" 2>/dev/null || true
[ -z "$(ls -A "$IS/psig/bin" 2>/dev/null)" ] || fail "a terminated install left temp files: $(ls -A "$IS/psig/bin")"

# Transport: an https base makes curl https-only with TLS 1.2+, redirects included.
mkdir -p "$IS/tbin"
printf '#!/bin/sh\necho "$*" >> "$IS_CURL_LOG"\nexit 22\n' > "$IS/tbin/curl"; chmod +x "$IS/tbin/curl"
IS_CURL_LOG="$IS/curl.log" PATH="$IS/tbin:$PATH" ROTA_RELEASE_BASE_URL="https://example.invalid/releases" \
  sh "$INSTALL" --prefix "$IS/ptr" >/dev/null 2>&1 || true
CL=$(cat "$IS/curl.log")
case $CL in *"--proto =https"*"--proto-redir =https"*"--tlsv1.2"*) ;; *) fail "curl should be pinned to https and TLS 1.2: $CL" ;; esac
: > "$IS/curl.log"
IS_CURL_LOG="$IS/curl.log" PATH="$IS/tbin:$PATH" ROTA_RELEASE_BASE_URL="file://$IS/rel" sh "$INSTALL" --prefix "$IS/ptr" >/dev/null 2>&1 || true
CL=$(cat "$IS/curl.log")
case $CL in *"--proto =file"*) ;; *) fail "a file:// base should limit curl to file: $CL" ;; esac
case $CL in *https*) fail "a file:// base should not allow https: $CL" ;; esac

# Bad flags and versions.
RC=0; sh "$INSTALL" --bogus >/dev/null 2>&1 || RC=$?; [ "$RC" != 0 ] || fail "unknown flag should fail"
RC=0; sh "$INSTALL" --version '1;rm' --prefix "$IS/p7" >/dev/null 2>&1 || RC=$?; [ "$RC" != 0 ] || fail "odd version should fail"
pass "install.sh verifies signature and sha256, fails closed, guards the URL"
unset ROTA_RELEASE_BASE_URL

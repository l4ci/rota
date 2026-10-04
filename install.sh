#!/bin/sh
# Install the rota binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
#
# Downloads rota_<os>_<arch>, its .minisig signature and checksums.txt from the
# release. It refuses a binary whose minisign signature does not verify against
# the release key embedded below, or whose sha256 does not match (fail closed:
# nothing is installed), then copies it to <prefix>/bin/rota. The signature is
# the authenticity check and needs the `minisign` tool; the checksum only
# catches a corrupt download, since checksums.txt comes from the same release.
#
# Options (flag wins over env), see --help:
#   --version X.Y.Z   ROTA_VERSION   release to install (default: latest)
#   --prefix DIR      ROTA_PREFIX    install to DIR/bin (default: $HOME/.local)
#
# Env:
#   ROTA_RELEASE_BASE_URL  default https://github.com/l4ci/rota/releases.
#                        Layout: <base>/latest/download/<asset> and
#                        <base>/download/v<version>/<asset>. https only; a
#                        file:// base is accepted for tests and needs curl.
#
# Transport: curl is used when present, limited to https (TLS 1.2 or newer,
# redirects included). wget is the fallback with --https-only; it is the weaker
# client, so prefer curl.
#
# Everything runs inside main(), called on the last line: a copy cut short
# mid-download (curl | sh) defines functions at most and installs nothing.
# No sudo, no shell-profile edits: it prints the PATH line if it is needed.
set -eu

# Release signing key (minisign, key id 2153154F7AA18B5D). Rotation: see
# .rota/RELEASE.md.
ROTA_MINISIGN_PUBKEY=RWRdi6F6TxVTIW92f3/QsWBl5VHdXm1FABgexyAla0z3A5WT4JzG6/SP

main() {
  die() {
    printf 'install.sh: %s\n' "$1" >&2
    [ -z "${2:-}" ] || printf 'hint: %s\n' "$2" >&2
    exit 1
  }

  usage() {
    cat <<'EOF'
Usage: install.sh [--version X.Y.Z] [--prefix DIR]

Installs the rota binary from a GitHub release, after verifying its minisign
signature and sha256. Needs the minisign tool (https://jedisct1.github.io/minisign/).

  --version X.Y.Z   release to install (env ROTA_VERSION; default: latest)
  --prefix DIR      install to DIR/bin (env ROTA_PREFIX; default: $HOME/.local)

Env ROTA_RELEASE_BASE_URL overrides the release location (https, or file:// for tests).
EOF
  }

  version=${ROTA_VERSION:-}
  prefix=${ROTA_PREFIX:-}
  while [ $# -gt 0 ]; do
    case $1 in
      --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
      --prefix) [ $# -ge 2 ] || die "--prefix needs a value"; prefix=$2; shift 2 ;;
      -h | --help) usage; exit 0 ;;
      *) die "unknown argument $1" "see install.sh --help" ;;
    esac
  done

  if [ -z "$prefix" ]; then
    [ -n "${HOME:-}" ] || die "HOME is not set" "pass --prefix DIR"
    prefix=$HOME/.local
  fi
  version=${version#v}
  case $version in
    *[!0-9A-Za-z.+-]*) die "bad version $version" ;;
  esac

  case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) die "unsupported OS $(uname -s)" ;; esac
  case $(uname -m) in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) die "unsupported CPU $(uname -m)" ;; esac

  root=${ROTA_RELEASE_BASE_URL:-https://github.com/l4ci/rota/releases}
  root=${root%/}
  # https or file://, and no userinfo anywhere (https://github.com@evil.example
  # is evil.example).
  scheme=
  case $root in
    *@*) ;;
    https://*) scheme=https ;;
    file://*) scheme=local ;;
  esac
  [ -n "$scheme" ] || die "refusing release URL $root" "use https without userinfo (file:// only for tests)"

  if [ -n "$version" ]; then base=$root/download/v$version; label=v$version
  else base=$root/latest/download; label="the latest release"
  fi
  asset=rota_${os}_${arch}

  # curl pins the transport: https only (the first request and every
  # redirect), TLS 1.2 at least. A file:// test base switches the first
  # request to file only.
  fetch() {
    if command -v curl >/dev/null 2>&1; then
      if [ "$scheme" = local ]; then
        curl -fsS --connect-timeout 10 --max-time 300 --proto '=file' -o "$2" "$1"
      else
        curl -fsSL --connect-timeout 10 --max-time 300 --proto '=https' --proto-redir '=https' --tlsv1.2 -o "$2" "$1"
      fi
    elif [ "$scheme" = local ]; then
      die "a file:// release base needs curl"
    elif command -v wget >/dev/null 2>&1; then
      wget -q --https-only --tries=1 --timeout=30 -O "$2" "$1"
    else
      die "need curl or wget"
    fi
  }

  bindir=$prefix/bin
  mkdir -p "$bindir" 2>/dev/null || die "cannot create $bindir" "pass --prefix with a writable directory"
  work=$(mktemp -d "$bindir/.rota-install.XXXXXX") || die "cannot write to $bindir" "pass --prefix with a writable directory"
  # A signal exits, which runs the EXIT trap: the temp dir never outlives us.
  trap 'rm -rf "$work"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP

  printf 'Downloading %s from %s\n' "$asset" "$label"
  fetch "$base/$asset" "$work/$asset" || die "download failed: $base/$asset" "check the version exists and is published"
  fetch "$base/$asset.minisig" "$work/$asset.minisig" || die "download failed: $base/$asset.minisig" "this release carries no signature; nothing installed"
  fetch "$base/checksums.txt" "$work/checksums.txt" || die "download failed: $base/checksums.txt"

  command -v minisign >/dev/null 2>&1 || die "need minisign to verify the download" "install it (brew install minisign, apt install minisign) and rerun"
  minisign -V -q -P "$ROTA_MINISIGN_PUBKEY" -m "$work/$asset" -x "$work/$asset.minisig" >/dev/null 2>&1 \
    || die "signature check failed for $asset; nothing installed" "the binary was not signed by the rota release key"

  want=$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1 }' "$work/checksums.txt")
  [ -n "$want" ] || die "checksums.txt has no entry for $asset"
  if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$work/$asset" | cut -d' ' -f1)
  elif command -v shasum >/dev/null 2>&1; then got=$(shasum -a 256 "$work/$asset" | cut -d' ' -f1)
  else die "need sha256sum or shasum to verify the download"
  fi
  [ "$got" = "$want" ] || die "checksum mismatch for $asset (want $want, got $got); nothing installed"

  chmod 755 "$work/$asset"
  # Same directory as the target, so the rename is atomic.
  mv -f "$work/$asset" "$bindir/rota" || die "cannot install $bindir/rota"
  printf 'Installed %s\n' "$bindir/rota"

  # shellcheck disable=SC2016  # $PATH is meant literally in the hint
  case ":${PATH:-}:" in
    *":$bindir:"*) ;;
    *) printf '\n%s is not on your PATH. Add it:\n  export PATH="%s:$PATH"\n' "$bindir" "$bindir" ;;
  esac
  printf '\nNext: rota skills install\n'
}

main "$@"

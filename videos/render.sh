#!/usr/bin/env bash
# Render a video episode (or all) from its checked-in script. See videos/README.md.
#
#   videos/render.sh <episode|all> [--setup]
#
# <episode> is a slug such as 01-why-rota (videos/episodes/NN-slug.md) or the
# pipeline fixture 00-fixture (videos/render/fixtures/). Stages, each cached:
#   1 capture  scripts/videos.py runs the terminal shots against $ROTA_BIN in a
#              throwaway project          -> videos/snapshots/<slug>.json
#   2 voice    Kokoro (CPU) speaks every vo: line, cached by text+voice
#                                         -> videos/.cache/audio/
#   3 compose  Remotion renders 16:9 and 9:16 mp4, a WebVTT caption file, and
#              stamps videos/out/manifest.json with the rota version + source hash
# Env: ROTA_BIN=<abs path> (default: go build ./cmd/rota into videos/.cache/rota)
#      RENDER_CONCURRENCY=2 (Chrome tabs; machine memory is shared, keep it low)
#      VIDEO_BUDGET_KB_16x9 / VIDEO_BUDGET_KB_9x16 override the size budget
set -euo pipefail

VIDEOS="$(cd "$(dirname "$0")" && pwd)"
REPO="$(dirname "$VIDEOS")"
R="$VIDEOS/render"
CACHE="$VIDEOS/.cache"
OUT="$VIDEOS/out"
CONC="${RENDER_CONCURRENCY:-2}"
# Per-episode size budget, from videos/STYLE.md: 16:9 12 MB (episode 1: 8 MB), 9:16 9 MB, captions 20 KB.
BUDGET_9x16="${VIDEO_BUDGET_KB_9x16:-9216}"
BUDGET_VTT=20

die() { echo "render.sh: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 not found: $2"; }

setup() {
  need node "install Node 20+"; need python3 "install Python 3.10+"; need ffmpeg "install ffmpeg"
  [ -d "$R/node_modules/remotion" ] || (cd "$R" && npm ci --no-audit --no-fund)
  if [ ! -x "$R/.venv/bin/python" ]; then
    python3 -m venv "$R/.venv"
    "$R/.venv/bin/pip" install -q -r "$R/requirements.txt"
  fi
  mkdir -p "$R/models"
  local base=https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0
  for f in kokoro-v1.0.int8.onnx voices-v1.0.bin; do
    [ -s "$R/models/$f" ] || curl -fL --retry 3 -o "$R/models/$f" "$base/$f"
  done
}

find_script() {
  for d in "$VIDEOS/episodes" "$R/fixtures"; do [ -f "$d/$1.md" ] && { echo "$d/$1.md"; return; }; done
  die "no script for '$1' (looked for videos/episodes/$1.md and videos/render/fixtures/$1.md)"
}

rota_bin() {
  if [ -n "${ROTA_BIN:-}" ]; then echo "$ROTA_BIN"; return; fi
  mkdir -p "$CACHE"
  (cd "$REPO" && go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$(tr -d '[:space:]' < VERSION)" -o "$CACHE/rota" ./cmd/rota) \
    || die "go build ./cmd/rota failed; set ROTA_BIN"
  echo "$CACHE/rota"
}

render_one() {
  local slug="$1" script bin work
  script="$(find_script "$slug")"
  bin="$(rota_bin)"
  work="$CACHE/work/$slug"
  rm -rf "$work"; mkdir -p "$work/public" "$OUT" "$VIDEOS/snapshots"

  echo "[$slug] capture"
  python3 "$REPO/scripts/videos.py" parse "$script" > "$work/script.json"
  python3 "$REPO/scripts/videos.py" capture "$script" --bin "$bin" --write "$VIDEOS/snapshots/$slug.json"

  echo "[$slug] voice"
  "$R/.venv/bin/python" - "$work/script.json" "$work/voice-plan.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
shots = [{"n": x["n"], "vo": x["speak"]} for x in s["shots"]]
shots.append({"n": "outro", "vo": "Find it on GitHub."})
json.dump({"shots": shots}, open(sys.argv[2], "w"))
PY
  "$R/.venv/bin/python" "$R/voice/synth.py" --plan "$work/voice-plan.json" --out "$work/public/audio"

  echo "[$slug] compose"
  mkdir -p "$work/public/fonts" "$work/public/brand"
  cp "$R"/public/fonts/*.woff2 "$work/public/fonts/"
  cp "$REPO/docs/images/rota-icon.svg" "$REPO/docs/images/rota-logo.svg" "$work/public/brand/"
  # The wordmark ships a light-on-dark media query; on a dark frame force the light fill.
  sed -i 's/path{fill:#1f2328}/path{fill:#f0f6fc}/' "$work/public/brand/rota-logo.svg"
  python3 - "$work" "$VIDEOS/snapshots/$slug.json" <<'PY'
import json, sys
w, snap = sys.argv[1], sys.argv[2]
script = json.load(open(w + "/script.json"))
audio = json.load(open(w + "/public/audio/audio.json"))["clips"]
props = {
    "script": {"slug": script["slug"], "shots": script["shots"]},
    "captures": json.load(open(snap))["captures"],
    "clips": [c for c in audio if c["n"] != "outro"],
    "outroClip": next(c for c in audio if c["n"] == "outro"),
}
json.dump(props, open(w + "/props.json", "w"))
PY
  local rn="$R/node_modules/.bin/remotion"
  for fmt in "Landscape:$slug.mp4" "Portrait:$slug.vertical.mp4"; do
    (cd "$R" && "$rn" render src/index.ts "${fmt%%:*}" "$OUT/${fmt#*:}" \
      --props="$work/props.json" --public-dir="$work/public" --concurrency="$CONC" \
      --log=warn --audio-codec=aac --crf=23 --pixel-format=yuv420p) || die "remotion render failed"
  done

  node "$R/vtt.mjs" "$work/props.json" > "$OUT/$slug.vtt"
  stamp "$slug" "$script" "$bin"
  budget "$slug"
}

stamp() { # manifest entry: rota version + source hash + output hashes
  python3 - "$OUT" "$1" "$2" "$3" <<'PY'
import hashlib, json, os, subprocess, sys
out, slug, script, rota = sys.argv[1:5]
h = lambda p: hashlib.sha256(open(p, 'rb').read()).hexdigest()
ver = subprocess.run([rota, 'version'], capture_output=True, text=True).stdout.split()[1]
mp = os.path.join(out, 'manifest.json')
m = json.load(open(mp)) if os.path.exists(mp) else {'episodes': {}}
m['episodes'][slug] = {'rota_version': ver, 'source_hash': h(script),
                       'outputs': {f: h(os.path.join(out, f)) for f in (slug + '.mp4', slug + '.vertical.mp4', slug + '.vtt')}}
m['episodes'] = dict(sorted(m['episodes'].items()))
json.dump(m, open(mp, 'w'), indent=1); open(mp, 'a').write('\n')
PY
}

budget() {
  local slug="$1" bad=0 kb b16=12288
  case "$slug" in 01-*) b16=8192 ;; esac
  b16="${VIDEO_BUDGET_KB_16x9:-$b16}"
  for pair in "$slug.mp4:$b16" "$slug.vertical.mp4:$BUDGET_9x16" "$slug.vtt:$BUDGET_VTT"; do
    kb=$(( ($(stat -c %s "$OUT/${pair%%:*}") + 1023) / 1024 ))
    if [ "$kb" -gt "${pair##*:}" ]; then
      echo "render.sh: ${pair%%:*} is ${kb} KB, over the ${pair##*:} KB budget in videos/STYLE.md" >&2; bad=1
    fi
  done
  [ "$bad" = 0 ] || exit 1
}

main() {
  [ $# -ge 1 ] || die "usage: videos/render.sh <episode|all> [--setup]"
  local target="$1"; shift
  [ "${1:-}" = "--setup" ] && { setup; return 0; }
  setup
  if [ "$target" = all ]; then
    local f; for f in "$VIDEOS"/episodes/*.md; do [ -e "$f" ] || die "no episodes in videos/episodes/"; render_one "$(basename "$f" .md)"; done
  else
    render_one "$target"
  fi
}
main "$@"

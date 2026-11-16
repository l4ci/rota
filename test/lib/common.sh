#!/usr/bin/env bash
# Helpers shared by the smoke sections (test/lib.sh) and the e2e scenarios
# (test/e2e/lib.sh): both source this file, so a fix lands once (#3423).

pass() { printf '  \033[32mOK\033[0m  %s\n' "$1"; }

# jget <path> reads one value from an envelope on stdin. The path is dotted
# with [n] indexes (data.items[0].id). Strings print raw, bools as true/false,
# everything else as compact JSON. A missing path prints nothing and fails.
jget() {
  python3 -c '
import json, re, sys
v = json.load(sys.stdin)
try:
    for k in re.findall(r"[^.\[\]]+|\[\d+\]", sys.argv[1]):
        v = v[int(k[1:-1])] if k[0] == "[" else v[k]
except (KeyError, IndexError, TypeError):
    sys.exit(1)
print(v if isinstance(v, str) else json.dumps(v, separators=(",", ":")))
' "$1"
}

# slot_field <workers.json> <slot> <field> prints one field of a slot; a missing
# or null field prints nothing, so test it with `-z`.
slot_field() {
  python3 -c 'import json,sys; s=[x for x in json.load(open(sys.argv[1]))["slots"] if x["name"]==sys.argv[2]][0]; v=s.get(sys.argv[3]); print("" if v is None else v)' "$1" "$2" "$3"
}

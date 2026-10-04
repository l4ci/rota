echo "global project registry: init registers, projects lists and marks missing (#24)"

# XDG_CONFIG_HOME points at a temp dir, so the real ~/.config/rota is never
# touched. No network, no forge.
GP="$(mktemp -d "$TMP/gproj.XXXXXX")"
export XDG_CONFIG_HOME="$GP/xdg"
mkdir -p "$GP/alpha" "$GP/beta"
gpv() { ( cd "$1" && shift && "$ROTA_BIN" --json "$@" 2>/dev/null ); }

OUT=$(gpv "$GP" projects) || fail "projects on an empty registry failed: $OUT"
[ "$(echo "$OUT" | jget 'data.projects' | tr -d ' \n')" = "[]" ] || fail "empty registry should list nothing: $OUT"

OUT=$(gpv "$GP/alpha" init --no-blocks) || fail "init alpha failed: $OUT"
[ "$(echo "$OUT" | jget data.projectRegistered)" = "true" ] || fail "first init should register: $OUT"
gpv "$GP/beta" init --no-blocks >/dev/null || fail "init beta failed"
[ -f "$XDG_CONFIG_HOME/rota/projects.json" ] || fail "registry not written under XDG_CONFIG_HOME"

OUT=$(gpv "$GP/alpha" init --no-blocks) || fail "re-init alpha failed: $OUT"
[ "$(echo "$OUT" | jget data.projectRegistered)" = "false" ] || fail "re-init should not re-register: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "re-init should stay a noop: $OUT"
[ "$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["projects"]))' "$XDG_CONFIG_HOME/rota/projects.json")" = "2" ] \
  || fail "re-init must not duplicate the entry"

OUT=$(gpv "$GP" projects) || fail "projects failed: $OUT"
[ "$(echo "$OUT" | jget 'data.projects[1].name')" = "beta" ] || fail "projects should list both: $OUT"

rm -rf "$GP/beta"
OUT=$(gpv "$GP" projects) || fail "projects after removal failed: $OUT"
case "$(cd "$GP" && "$ROTA_BIN" projects 2>/dev/null)" in *"beta"*"(missing)"*) ;; *) fail "text mode should mark the missing path" ;; esac
[ "$(echo "$OUT" | python3 -c 'import json,sys; print(sorted((p["name"],p["missing"]) for p in json.load(sys.stdin)["data"]["projects"]))')" = "[('alpha', False), ('beta', True)]" ] \
  || fail "beta should be flagged missing and kept: $OUT"
unset XDG_CONFIG_HOME

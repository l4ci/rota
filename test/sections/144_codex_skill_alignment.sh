echo "Codex skill output contracts (#585; scripted worker, no model)"
TMP_CSA="$(mktemp -d)"
trap 'rm -rf "$TMP_CSA"' EXIT
mkdir -p "$TMP_CSA/project/.rota" "$TMP_CSA/bin"
cp "$TESTDIR/fakes/codex" "$TESTDIR/fakes/codex_skill_worker.py" "$TMP_CSA/bin/"
(
  cd "$TMP_CSA/project"
  git init -q -b main
  printf '{"backlog":{"backend":"file"}}\n' > .rota/config.json
  printf '# Backlog\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  git add .rota/config.json .rota/BACKLOG.md
  git commit -qm seed
  "$ROTA_BIN" skills install --scope project --agent codex >/dev/null
  "$ROTA_BIN" item create --kind bugs --title 'Preview fixture' --tag P2 --desc 'A contained fixture bug.' >/dev/null
  git add .rota .agents
  git commit -qm fixture

  # Explicit fake PATH and opt-in: a missing fake cannot fall through to Codex.
  csa_worker() {
    PATH="$TMP_CSA/bin:$PATH" ROTA_FAKE_CODEX_SKILLS=1 ROTA_BIN="$ROTA_BIN" \
      "$TMP_CSA/bin/codex" exec --json "$1"
  }
  csa_worker '/rota-work --preview B01' > "$TMP_CSA/preview.jsonl"
  [ -z "$(git status --porcelain --untracked-files=all)" ] || fail "preview wrote project state"
  [ "$(git rev-list --count HEAD)" = 2 ] || fail "preview made a commit"
  csa_worker '/rota-capture task: Add a fixture example' > "$TMP_CSA/capture.jsonl"
  # Native Codex spelling reaches the same skill and returns the next minted ID.
  csa_worker '$rota-capture task: Add another fixture example' > "$TMP_CSA/capture2.jsonl"
  "$ROTA_BIN" --json backlog list > "$TMP_CSA/backlog.json"

  python3 - "$TMP_CSA" <<'PY'
import json, pathlib, re, sys
root = pathlib.Path(sys.argv[1])
def message(name):
    events = [json.loads(line) for line in (root / name).read_text().splitlines()]
    assert events[-1]["type"] == "turn.completed", events
    messages = [e["item"]["text"] for e in events if e["type"] == "item.completed" and e["item"]["type"] == "agent_message"]
    assert len(messages) == 1 and messages[0].strip(), events
    return messages[0]
peek = message("preview.jsonl")
assert re.match(r"Peek for B01:\n", peek), peek
headings = ["Approach", "Files I'd touch", "Files I'd create", "Tests I'd add", "Assumptions I'm making", "Known unknowns"]
positions = [peek.index("\n" + heading + "\n") for heading in headings]
assert positions == sorted(positions), peek
for i, start in enumerate(positions):
    end = positions[i + 1] if i + 1 < len(positions) else len(peek)
    assert peek[start:end].split("\n", 2)[2].strip(), peek
assert not re.search(r"<[^>]+>|\n(?:Repo|Hard boundaries to respect)\n", peek), peek
items = json.loads((root / "backlog.json").read_text())["data"]["tasks"]
assert len(items) == 2, items
seen = set()
for filename in ["capture.jsonl", "capture2.jsonl"]:
    report = message(filename)
    match = re.fullmatch(r"- \[(T\d+)\] (.+)", report)
    assert match, report
    assert any(item["id"] == match[1] and item["title"] == match[2] for item in items), report
    seen.add(match[1])
assert len(seen) == 2, seen
PY
  [ "$(git rev-list --count HEAD)" = 2 ] || fail "capture started implementation or committed"
  [ ! -f .rota/status.json ] || fail "skills registered implementation work"

  # Discovery is a real dependency: the worker refuses a missing skill.
  mv .agents/skills/rota-capture/SKILL.md "$TMP_CSA/capture-skill.md"
  if csa_worker '/rota-capture task: Must not be created' > "$TMP_CSA/missing.out" 2>&1; then
    fail "Codex stub ran a missing skill"
  fi
  "$ROTA_BIN" --json backlog list > "$TMP_CSA/after-missing.json"
  cmp -s "$TMP_CSA/backlog.json" "$TMP_CSA/after-missing.json" || fail "missing skill mutated backlog"
)
rm -rf "$TMP_CSA"
trap 'rm -rf "$TMP"' EXIT
pass "Codex stub preview is read-only; capture reports created IDs/titles and stops"

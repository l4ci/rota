# External knowledge

Some learnings hold in every project that uses a tool: how Go tests behave, how git worktrees share state, a GitHub Actions quirk. `.rota/KNOWLEDGE.md` keeps them in one project. The external knowledge base keeps them in a separate git repo of markdown files, cloned once on your machine and shared by every project.

It is off until you set one key.

## Set it up

1. Create a git repo for the knowledge, with a remote (GitHub, GitLab, any host your git credentials reach). The sample under [`docs/examples/knowledge-repo/`](../examples/knowledge-repo/) is the layout to start from:

   ```bash
   cp -R docs/examples/knowledge-repo ~/knowledge
   cd ~/knowledge
   git init -b main && git add . && git commit -m "Start the knowledge base"
   git remote add origin <url> && git push -u origin main
   ```

2. Tell rota where the clone is. The key is machine-wide, so set it in the global config:

   ```bash
   rota config set knowledge.external ~/knowledge --global
   ```

3. Check it:

   ```bash
   rota doctor        # the "external knowledge" line
   rota knowledge topics --external
   ```

On another machine, clone the same repo and set the key there.

## Layout

| File | Holds |
|------|-------|
| `README.md` | One line per topic: `- [Go](topics/go.md) — hook`. The name is the topic, the hook says what is in it. It renders as the repo front page on GitHub and GitLab. An older base that has only `INDEX.md` keeps working: rota reads and updates that file until you rename it. |
| `topics/<slug>.md` | The topic's bullets, in the same shape as `KNOWLEDGE.md`: `- **Title** — body <!-- 2026-10-12 source:rota -->`. `source:` names the project the learning came from; an optional `seen:godot@4.6,swift@6` lists the tool versions it was seen on (see [Tool versions](#tool-versions)). |
| `topics/<slug>/README.md`, `topics/<slug>/<sub>.md` | A topic that was split (see [Split a big topic](#split-a-big-topic)). The root line points at the folder's `README.md`, which holds the topic's intro, a sub-index (`- [Networking](networking.md) — hook`) and any bullets not yet given a subtopic. Each `<sub>.md` holds one subtopic's bullets. |
| `rota-knowledge.json` | How writes land (below). Optional. |

Topic names are technology names (Go, git, GitHub Actions), not project names. External bullets have no tier or hit lifecycle: they are written by a learn pass that already judged them.

## Read it

```bash
rota knowledge query --external Go "GitHub Actions"
rota knowledge topics --external
```

A split topic reads its sub-index and the bullets without a subtopic. `--all` adds a section per subtopic, and a subtopic is named like its heading, `Topic: Subtopic` (`rota knowledge query --external "Godot: Networking"`, or just `Networking` when that is unambiguous):

```bash
rota knowledge query --external Godot            # sub-index + bullets without a subtopic
rota knowledge query --external --all Godot      # also every subtopic
rota knowledge stats --external                  # bullets and bytes per topic file
```

Before it reads, rota makes sure the clone is current. It fetches at most once per `knowledge.externalTTL` minutes (default 10; the stamp lives in the rota state dir), then fast-forwards when the remote is ahead. It never resets anything:

- A clone with uncommitted changes, or one that has diverged from its remote, is read as-is, with a one-line warning.
- An unreachable remote prints `reading stale copy` and reads the local files.

Skills use the same call: [`/rota-learn`](learning.md) and the knowledge consult step of `/rota-debug`, `/rota-review`, `/rota-refactor` and `/rota-work` query the external base alongside the project's when the key is set.

## Write it

```bash
printf '%s' "$BODY" | rota knowledge add --external --topic Go --title "Per-case test names" --body-file -
rota knowledge amend --external --topic Go --fragment "Per-case test names" --mode append --body-file -
```

A write needs a clean clone. It pulls (fast-forward only), writes the bullet to `topics/<slug>.md`, adds the topic and its `README.md` line when the topic is new (`--hook` sets the line, default the bullet title), commits and then follows the repo's policy in `rota-knowledge.json`:

```json
{"write": "push", "branch": "main"}
```

| `write` | What happens |
|---------|--------------|
| `push` | `git push` to `branch`. A rejected push gets one `git pull --rebase` and a retry. |
| `pr` | Pushes a `rota-knowledge/<topic>-<time>` branch and runs `gh pr create` against `branch`. No confirmation gate; the clone returns to its branch. |

`branch` is optional. Without it, a push lands on the branch the clone tracks (its upstream, else its current branch), so a repo whose default branch is `master` works. A `push` policy refuses to write when the clone is on a different branch than `branch`. Titles may not contain `*`; titles, bodies and amend text must be one line and may not contain `-->`. Rota refuses (exit 4) rather than write through a symlink or over an untracked file.

Without a `rota-knowledge.json` the repo is treated as `{"write": "push"}`, and the verb says so in its output. `rota doctor` warns about the missing file.

## Split a big topic

A topic read in full costs every consumer its whole file. `rota doctor` warns (`external knowledge size`) when a topic passes `knowledge.externalMaxBullets` or `knowledge.externalMaxBytes`, or the index passes `knowledge.externalMaxTopics`; `rota knowledge stats --external` shows the numbers. `/rota-learn --external-review` drafts the split for you: it groups the bullets by theme into 3 to 7 subtopics, shows the plan, and always asks before it runs the verb, whatever `autonomy.level` says.

The verb takes a plan, a JSON file naming each subtopic and the bold titles of the bullets that move into it:

```bash
rota knowledge split --external --topic Godot --plan plan.json
```

```json
{"subtopics": [
  {"name": "Networking", "hook": "multiplayer, RPCs, lobbies", "titles": ["RPC authority", "Lobby code"]},
  {"name": "Core", "hook": "autoloads and signals", "titles": ["Autoload order", "Signals"]}
]}
```

It checks the whole plan first (an unknown title, a title in two subtopics, an empty or duplicate subtopic name, or a topic that is itself a subtopic is refused and nothing is written), then writes everything in one commit, `knowledge: split Godot into 2 subtopics`, and lands it by the repo's policy:

- `topics/godot.md` becomes `topics/godot/README.md` (title, any intro prose, the sub-index, then the bullets the plan did not list) plus `topics/godot/networking.md` and `topics/godot/core.md`. The root `README.md` line keeps its hook and points at the folder.
- Bullets move byte for byte: wrapped bullets keep their continuation lines and every stamp stays. The verb refuses to write if one bullet line would be lost.
- The same plan again changes nothing (`changed: false`). On a topic that is already split, a plan adds subtopics or moves more README bullets into one.
- After a split, `add --topic "Godot: Networking"` writes into a subtopic, and `add --topic "Godot: Lobby"` creates one. `add --topic Godot` lands in the README below the sub-index. Subtopics nest one level: `A: B: C` is refused, and so is `add --topic "Shell: Zsh"` while Shell is still flat (split it first).
- `amend`, `replace`, `rm` and `migrate external-knowledge` reach subtopics the same way.

### Older rota on a split base

**Every machine that writes to the base needs a rota that knows split topics before the first split.** An older rota cannot see `topics/godot/`: its `add --topic Godot` creates a flat `topics/godot.md` and a second root line `- [Godot](topics/godot.md)`. That is the stray file. New rota never writes to it, still prints its bullets under `## Godot (stray: topics/godot.md)` with a warning, lists it in `stats` (`stray: true`) and in `rota doctor` (`external knowledge layout`). Fold it back with the same verb, no plan needed:

```bash
rota knowledge split --external --topic Godot
```

The stray bullets join the README (or the subtopics of a `--plan` that lists them), the stray file and its root line go in the same commit, and the result reports `folded: N`. A stray bullet whose title already exists in the folder is kept once when the two lines are byte-identical; when they differ, the verb exits 4 naming both files and you reconcile them by hand.

## Tool versions

A learning that depends on a tool version carries it in the stamp:

```bash
printf '%s' "$BODY" | rota knowledge add --external --topic Godot --title "Tween leak" --seen godot@4.6 --body-file -
```

`--seen` takes `tool@version` entries separated by spaces or commas. Tool names are lowercase. A bare tool (`--seen godot`) takes its version from the project's manifests and exits 3 naming the tool when none is found. `query --external` marks a bullet whose project runs a newer major.minor, for example `(seen on godot 4.6, project on 4.8: recheck)`.

## Edit a bullet

Three verbs change a bullet that is already there. Each needs a clean, current clone, edits one bullet, commits with no trailer and lands by the repo's policy like `add`, printing where it went. A rejected push gets the same single `git pull --rebase` retry.

```bash
rota knowledge amend --external --topic Godot --fragment "Tween leak" --seen godot@4.8 --date 2026-10-26
rota knowledge replace --external --topic Godot --old "Kill tweens" --new "Free tweens"
rota knowledge rm --external --topic Godot --title "Tween leak"
```

| Verb | Edits | Exit codes |
|------|-------|------------|
| `amend --fragment F` with `--seen` and/or `--date` | The bullet's stamp. `--mode append --body-file` stays optional and can ride along; at least one of body, `--seen`, `--date` is required. `--seen` replaces the list, `--seen ""` drops it. `--date` rewrites the date. A bullet with no stamp gets one only when `--date` is given. Other stamp fields (`source:`) are kept as written. | 2 for a missing edit, an invalid `--seen` or a `--date` that is not a real `YYYY-MM-DD`, or `--seen` alone on an unstamped bullet; 3 for no bullet; 4 for several |
| `replace --old X --new Y` | Title and body text. A stamp is never matched: an `--old` that sits only in it, or spans body and stamp, matches nothing. | 3 for no match; 4 for several bullets, or a `--new` with `-->` or a line break |
| `rm --title S` | Deletes the bullet. | 3 for an unknown title; 4 for several with that title |

All three exit 4 on a dirty or diverged clone and write nothing. `rename-topic` has no `--external` form. Git history keeps what an edit or removal replaced.

## Move old `(seen on …)` suffixes

Bullets written before `seen:` existed carry their tool version as a trailing `(seen on Godot 4.6)` in the body, which `query` cannot compare. `rota migrate external-knowledge` moves each one into the stamp.

```sh
rota migrate external-knowledge          # preview: nothing is written
rota migrate external-knowledge --apply  # one commit, landed by the repo's policy
```

| Suffix | Becomes |
|--------|---------|
| `(seen on Godot 4.6)` | `seen:godot@4.6` |
| `(seen on Swift 6, macOS 26)` | `seen:swift@6,macos@26` |
| `(seen on three.js r186)` | `seen:three.js@r186` |
| `(seen on 26.5)` under Xcode | `seen:xcode@26.5`, marked `inferred` in the preview (a suffix with no tool takes the topic's name) |

Tool names are lowercased, spaces become `-`, and versions stay as written. A trailing `.` after the closing parenthesis stays as the sentence's period. A bullet that already has `seen:` keeps its versions and gains the tools it lacks; when the stamp and the suffix name different versions of one tool, the whole bullet is left as written and listed as `manualReview` ("stamp says X, suffix says Y"). A `(seen on …)` that is not at the end of the bullet, sits on a continuation line, or has no version is not touched: the preview and `--apply` list it as `manualReview` for you to fix with `replace --external`.

The preview reads the clone like `query` does, so a dirty clone is fine. `--apply` needs a clean, current clone (exit 4 when dirty or diverged, exit 5 when the remote is unreachable) and writes one commit, `knowledge: move (seen on …) into seen: stamps`, with no trailer. A second `--apply` reports `changed: false` and commits nothing. If a run finds nothing to change but the clone is ahead of its remote (an earlier apply whose push failed), it says so and names `git -C <clone> push`.

To roll back, revert the commit: `git -C <clone> revert <sha>` and push. In `pr` mode the change sits on a `rota-knowledge/seen-stamps-…` branch with an open PR; close the PR instead.

## Settings

| Key | Default | Meaning |
|-----|---------|---------|
| [`knowledge.external`](../reference/config-options.md) | empty | Path of the local clone. Empty turns the feature off. |
| [`knowledge.externalTTL`](../reference/config-options.md) | `10` | Minutes a fetch stays fresh. `0` fetches on every read. |

| [`knowledge.externalMaxBullets`](../reference/config-options.md) | `25` | A topic file over this many bullets makes `rota doctor` warn. `0` turns the check off. |
| [`knowledge.externalMaxBytes`](../reference/config-options.md) | `8192` | The same for a topic file's size in bytes. |
| [`knowledge.externalMaxTopics`](../reference/config-options.md) | `60` | The same for the number of topics in the index. |

`--external` with the key unset exits 3 and names `knowledge.external`.

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
| `rota-knowledge.json` | How writes land (below). Optional. |

Topic names are technology names (Go, git, GitHub Actions), not project names. External bullets have no tier or hit lifecycle: they are written by a learn pass that already judged them.

## Read it

```bash
rota knowledge query --external Go "GitHub Actions"
rota knowledge topics --external
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

## Settings

| Key | Default | Meaning |
|-----|---------|---------|
| [`knowledge.external`](../reference/config-options.md) | empty | Path of the local clone. Empty turns the feature off. |
| [`knowledge.externalTTL`](../reference/config-options.md) | `10` | Minutes a fetch stays fresh. `0` fetches on every read. |

`--external` with the key unset exits 3 and names `knowledge.external`.

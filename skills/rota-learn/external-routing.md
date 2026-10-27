# External knowledge routing for `/rota-learn`

Loaded by `skills/rota-learn/SKILL.md` Step 3 when `knowledge.external` has a path set.

With a path set, decide per learning. **Project:** it names this repo's files, verbs, config or conventions. **External:** it holds in any repo that uses the tool (Go, git, herdr, GitHub Actions, Postgres). State each call in one line, for example `-> external (Go): t.Run needs a case name`. When `autonomy.level` (see [`references/autonomy.md`](references/autonomy.md)) is `auto` you decide; otherwise ask once for the whole batch, with your calls as the default answer.

Project learnings continue through Steps 4 to 6. For each external one:

1. Reuse a topic from `rota knowledge topics --external` before minting one (technology names, not project names).
2. Dedupe: run `rota knowledge query --external "<topic>"`. A bullet that already says it is skipped; a sharper version is `rota knowledge amend --external`. Query marks a bullet `(seen on <tool> X, project on Y: recheck)` when the project runs a newer tool version than the bullet was seen on. If this session confirmed such a bullet, run `rota knowledge amend --external --topic "<Technology>" --fragment "<title>" --seen "<tool>"` (a bare tool takes the project's version; no body needed). `--seen` replaces the whole list, so pass every tool the bullet carries (e.g. `--seen "godot swift"`) or the others are dropped. A bullet the session found wrong is fixed with `rota knowledge replace --external --topic "<Technology>" --old "<text>" --new "<text>"` or dropped with `rota knowledge rm --external --topic "<Technology>" --title "<title>"`; git history keeps the old text.
3. Write it. Add `--seen <tool>` when the learning depends on a tool version:

```bash
printf '%s' "$BODY" | rota knowledge add --external --topic "<Technology>" --title "<Short rule title>" [--seen "<tool>"] --body-file -
```

The verb pulls, writes `topics/<slug>.md` (and the `README.md` line for a new topic), commits, then pushes or opens a PR by the repo's own policy, and prints which. It exits 4 on a dirty or diverged clone: report that in Step 8 and keep the learning for a rerun. External bullets carry no sha, no tier and no managed block, so Steps 5 and 6 do not apply to them.

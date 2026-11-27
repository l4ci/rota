---
subsystem: knowledge-and-map
summary: project knowledge, decisions and glossary files, the external knowledge repo, topic-name matching, the subsystem map and QA indexes, staleness listing and the shared frontmatter parser
touched: 2026-11-27
last_mapped_commit: f5d97a0faf3575c5fa31e8c2306bf31b0ca1562e
---

## Purpose

Reads and writes the tracked `.rota/` text stores that hold durable project memory: topic bullets in `KNOWLEDGE.md`, decisions, the glossary, `.rota/map/` and `.rota/qa/` files, plus the shared external knowledge repo. `topicmatch` holds the three rules that map a requested topic name to stored topics. `stale` lists entries gone stale across the backlog, knowledge and map. `frontmatter` is the one parser of the flat `---` header those files use.

Depends on (by map file): foundations (fsio, git, jsonx, pystr, stamp, strutil, bulletfmt, pathglob), config-and-setup (config, rotatree, rotastate, toolver). Also imports backlog-items (`section`, `backlog`) and cli-dispatch (`exitmap`, from `stale`). Used by cli-dispatch, config-and-setup (initproj, migrate) and doctor-and-reap (doctor).

## Entry points
- internal/knowledge/scope.go:30 — `Store`: locates the knowledge files of one project (root plus sub-repo map)
- internal/knowledge/query.go:26 — `Store.Query`: topic read by scope with tier filtering
- internal/knowledge/ops.go:72 — `Store.Add`: write a bullet (title, body, date, sha, paths)
- internal/extknowledge/fresh.go:127 — `Clone.Freshen`: bounded fetch and fast-forward before a read
- internal/extknowledge/read.go:191 — `Clone.QueryWith`: read external topics; write side is `Clone.Add` at write.go:74
- internal/topicmatch/topicmatch.go:29 — `Headings`; `ReadIndex` :72, `WriteIndex` :94, `Slugify` :14
- internal/mapqa/mapqa.go:39 — `Query`: bodies of named `.rota/map` or `.rota/qa` files; `Stats` at :211
- internal/stale/stale.go:71 — `FindReport`: stale entries by kind (`Kinds` at :24: `map`, `knowledge`, `todo`) and the count of skipped undated todo items
- internal/frontmatter/frontmatter.go:53 — `Parse`: frontmatter map, list keys and body; `Fence` at :28, `SetField` at :126

## Key files / dirs
- `internal/knowledge/` — `scope.go` (Store, `Umbrella`, `ErrScope`), `query.go`, `ops.go`, `paths.go` (path-scoped bullets, matched with `pathglob.Match`), `tier.go` (sidecar, tiers provisional/confirmed/deprecated), `bullet.go` (on `bulletfmt`; title lookups go through `bulletfmt.ResolveTitle`, and `Stats` counts bullets with `bulletfmt.Count`), `hit.go` and `hitbuffer.go` (gitignored `knowledge-hits.jsonl`), `decisions.go`/`decisionwrite.go`/`decisionentry.go`, `glossary.go`, `block.go`, `instructions.go` with `skills_block.md`, `which.go`
- `internal/extknowledge/` — external repo (`README.md`, `topics/<slug>.md`, `rota-knowledge.json`): `read.go`, `write.go`, `edit.go` (Remove, Replace; title lookup through `bulletfmt.ResolveTitle`), `split.go`, `fresh.go`, `hits.go` (per-machine log in the state dir), `state.go`, `stats.go`, `lifecycle.go`, `migrate.go`, `bullet.go`, `extknowledge.go`
- `internal/topicmatch/` — `topicmatch.go`: heading, read-index and write-index matching; used by `knowledge` (`query.go`, `decisions.go`) and extknowledge read/write
- `internal/mapqa/` — `mapqa.go`: per-name reads, managed index blocks (`MapIndexBlock`, `QAIndexBlock`, `WriteIndex`), size stats (`Stats`) and stale/cap notes (`StaleNote`, `CapNote`)
- `internal/stale/` — `stale.go`: `Kinds`, `FindReport`, `TodoSource`; a knowledge file git cannot blame (untracked) yields none, a blame that fails to run is an error
- `internal/frontmatter/` — `frontmatter.go`: `Parse`, `Str`, `UpdateField`, `SetField`; `render.go`: `Render` (fields back to a frontmatter block)

## Stack
Go 1.22, standard library; markdown and JSON files on disk. `extknowledge` shells out to git through `proc`/`git`. Dates through `stamp.ParseDate`.

## Conventions specific here
- Invariants: `frontmatter` understands only `key: value` and inline lists `key: [a, b]` between `---` lines (no nesting, no quoting); file formats stay byte-compatible with the earlier Python helpers.
- Topic-name matching differs on purpose by operation and store (headings, read index, write index); each rule is one named function in `topicmatch`.
- A knowledge hit appends a JSON line to the gitignored `knowledge-hits.jsonl` instead of rewriting the tier sidecar; `rota knowledge tier fold` applies the buffer.
- Glossary terms are canonical names and are never tiered (`GlossaryTopic`).
- `Freshen` never resets: a dirty or diverged clone or an unreachable remote returns a one-line warning and the caller reads what is on disk. Hits for the external clone live in the state dir, never in the clone.
- A source that exists but cannot be read or parsed (knowledge block, map/QA files, external topic files) returns `fsio.Unreadable(err)`, which the CLI exits 5 for; a topic indexed but not yet written is skipped, any other unreadable file is an error. `extknowledge` has one topic traversal (`Clone.walk`, walk.go:41) behind both query and match.
- Scope errors wrap `ErrScope` so callers map them to the resolution exit code.
- The map and QA index blocks are written with `section.UpsertManaged` (`mapqa.WriteIndex`).

## Test commands
- `go test ./internal/knowledge/ ./internal/extknowledge/ ./internal/topicmatch/ ./internal/mapqa/ ./internal/stale/ ./internal/frontmatter/`
- CLI surface: `go test ./internal/cli/ -run 'Knowledge|Map|Stale'`

## Concerns
- `knowledge` and `extknowledge` both implement topic query, add and hit with different storage; changes to bullet format touch both (shared rendering now in `bulletfmt`).
- `stale.FindReport` kind `todo` lists open items through the caller-supplied `TodoSource` (stale.go:52; any `backlog.Backend` satisfies it), so staleness follows the backend the project selected, issue tracker included; a nil source lists none. Items without a usable Captured date are skipped, and `FindReport` counts them in `SkippedUndated`.

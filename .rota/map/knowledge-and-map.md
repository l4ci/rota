---
subsystem: knowledge-and-map
summary: project knowledge, decisions and glossary files, the external knowledge repo, topic-name matching, the subsystem map and QA indexes, staleness listing and the shared frontmatter parser
touched: 2026-11-09
last_mapped_commit: a5ed8bdf14da0c12adfae97aaa7718668d37e726
---

## Purpose

Reads and writes the tracked `.rota/` text stores that hold durable project memory: topic bullets in `KNOWLEDGE.md`, decisions, the glossary, `.rota/map/` and `.rota/qa/` files, plus the shared external knowledge repo. `topicmatch` holds the three rules that map a requested topic name to stored topics. `stale` lists entries gone stale across the backlog, knowledge and map. `frontmatter` is the one parser of the flat `---` header those files use.

Depends on (by map file): foundations (fsio, git, jsonx, pystr, stamp, strutil, bulletfmt, pathglob), config-and-setup (config, rotatree, toolver). Also imports backlog-items (`section`, `backlog`) and cli-dispatch (`exitmap`, from `stale`). Used by cli-dispatch, config-and-setup (initproj, migrate) and doctor-and-reap (doctor).

## Entry points
- internal/knowledge/scope.go:30 — `Store`: locates the knowledge files of one project (root plus sub-repo map)
- internal/knowledge/query.go:25 — `Store.Query`: topic read by scope with tier filtering
- internal/knowledge/ops.go:71 — `Store.Add`: write a bullet (title, body, date, sha, paths)
- internal/extknowledge/fresh.go:127 — `Clone.Freshen`: bounded fetch and fast-forward before a read
- internal/extknowledge/read.go:185 — `Clone.Query`: read external topics; write side is `Clone.Add` at write.go:91
- internal/topicmatch/topicmatch.go:29 — `Headings`; `ReadIndex` :55, `WriteIndex` :77, `Slugify` :14
- internal/mapqa/mapqa.go:30 — `Query`: bodies of named `.rota/map` or `.rota/qa` files; `Stats` at :161
- internal/stale/stale.go:64 — `Find`: stale entries by kind (`map`, `knowledge`, `todo`)
- internal/frontmatter/frontmatter.go:23 — `Parse`: frontmatter map, list keys and body; `SetField` at :109

## Key files / dirs
- `internal/knowledge/` — `scope.go` (Store, `Umbrella`, `ErrScope`), `query.go`, `ops.go`, `paths.go` (path-scoped bullets, matched with `pathglob.Match`), `tier.go` (sidecar, tiers provisional/confirmed/deprecated), `bullet.go` (on `bulletfmt`), `hit.go` and `hitbuffer.go` (gitignored `knowledge-hits.jsonl`), `decisions.go`/`decisionwrite.go`/`decisionentry.go`, `glossary.go`, `block.go`, `instructions.go` with `skills_block.md`, `which.go`, `resolve.go`
- `internal/extknowledge/` — external repo (`README.md`, `topics/<slug>.md`, `rota-knowledge.json`): `read.go`, `write.go`, `edit.go` (Remove, Replace), `split.go`, `fresh.go`, `hits.go` (per-machine log in the state dir), `state.go`, `stats.go`, `lifecycle.go`, `migrate.go`, `bullet.go`, `extknowledge.go`
- `internal/topicmatch/` — `topicmatch.go`: heading, read-index and write-index matching; used by knowledge/resolve.go and extknowledge read/write
- `internal/mapqa/` — `mapqa.go`: per-name reads, managed index blocks (`MapIndexBlock`, `QAIndexBlock`, `WriteIndex`), size stats and stale/cap notes
- `internal/stale/` — `stale.go`: `Kinds`, `Find`
- `internal/frontmatter/` — `frontmatter.go`: `Parse`, `Str`, `UpdateField`, `SetField`

## Stack
Go 1.22, standard library; markdown and JSON files on disk. `extknowledge` shells out to git through `proc`/`git`. Dates through `stamp.ParseDate`.

## Conventions specific here
- Invariants: `frontmatter` understands only `key: value` and inline lists `key: [a, b]` between `---` lines (no nesting, no quoting); file formats stay byte-compatible with the earlier Python helpers.
- Topic-name matching differs on purpose by operation and store (headings, read index, write index); each rule is one named function in `topicmatch`.
- A knowledge hit appends a JSON line to the gitignored `knowledge-hits.jsonl` instead of rewriting the tier sidecar; `rota knowledge tier fold` applies the buffer.
- Glossary terms are canonical names and are never tiered (`GlossaryTopic`).
- `Freshen` never resets: a dirty or diverged clone or an unreachable remote returns a one-line warning and the caller reads what is on disk. Hits for the external clone live in the state dir, never in the clone.
- Scope errors wrap `ErrScope` so callers map them to the resolution exit code.
- The map and QA index blocks are written with `section.UpsertManaged` (`mapqa.WriteIndex`).

## Test commands
- `go test ./internal/knowledge/ ./internal/extknowledge/ ./internal/topicmatch/ ./internal/mapqa/ ./internal/stale/ ./internal/frontmatter/`
- CLI surface: `go test ./internal/cli/ -run 'Knowledge|Map|Stale'`

## Concerns
- `knowledge` and `extknowledge` both implement topic query, add and hit with different storage; changes to bullet format touch both (shared rendering now in `bulletfmt`).
- `stale.Find` kind `todo` reads the file backlog (`backlog.File{Root}.Markdown`), the frozen `.rota/BACKLOG.md`, not the issue tracker.

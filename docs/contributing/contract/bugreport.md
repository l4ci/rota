---
refs:
  - internal/bugreport
  - internal/cli/bugreport.go
  - internal/gate
---

## #1175: bugreport

`rota bugreport` is the local log of rota's own mistakes and their gated filing on the rota repo. The log is `.rota/bugreports.jsonl` (per-developer, gitignored): one JSON line per record `{"id": "bug-<n>", "ts", "source": "auto"|"agent", "verb", "args", "exit", "error", "summary", "expected", "harness", "model", "count", "status": "open"|"filed"|"dismissed", "issue", "offered"}`. An invocation that ends in an internal error or a panic (exit 70) appends an `auto` record by itself, unless the verb is under `bugreport`; an open `auto` record with the same verb, exit and error is counted (`count`) instead of added again. Other exit codes record nothing. `rota round wind-down` (a `bugreports` count in `data` and one text line) and the Stop hook of a solo session (a `systemMessage`, once per record, silent under `ROTA_SLOT`) offer the open records without blocking.

Every draft is redacted before it is shown or filed: tokens and keys, email addresses, home-directory paths, the hostname, and the project's own repo name and remote URL. It carries the rota version, OS and architecture, harness and model (the flags, then the record, then the slot named by `ROTA_SLOT`, else `unknown`).

### rota bugreport add
rota bugreport add --summary <text> [--expected <text>] [--verb <verb>] [--harness <kind>] [--model <name>]
repo: none
data: {"id": string, "source": "agent", "status": "open", ...}
exit: 2 when --summary is missing; 3 when no `.rota/` is found

### rota bugreport list
rota bugreport list
repo: none
data: {"records": [{"id", "source", "verb", "exit", "summary", "error", "count", "status", "issue"}]} (open records only)
exit: 3 when no `.rota/` is found

### rota bugreport draft
rota bugreport draft <id> [--harness <kind>] [--model <name>]
repo: none
data: {"id": string, "title": string, "body": string}
exit: 2 without exactly one id; 3 for an unknown id
note: text mode prints `# <title>`, a blank line and the body. The body follows `skills/rota-bugreport/issue-template.md`.

### rota bugreport duplicates
rota bugreport duplicates <id>
repo: none
data: {"id": string, "duplicates": [{"number", "title", "url"}]}
exit: 2, 3 as draft; tracker (5 when `gh` is missing or not authenticated)
note: searches the open issues of the upstream repo (`$ROTA_UPSTREAM_REPO`, else `l4ci/rota`) with the redacted title.

### rota bugreport file
rota bugreport file <id> [--comment <number>] [--harness <kind>] [--model <name>] --confirm --confirm-note <answer>
repo: none
data: {"id": string, "url": string, "upstreamRepo": string, "changed": true}
exit: 2 as draft, or a confirmation flag given without the other; 3 for an unknown id; 4 when the `bugreport-file` gate is not cleared (checked after the arguments, before `gh` runs), or the record is already filed and no `--comment` is given; 5 when `gh` is missing, not authenticated or failing (the hint carries the manual `issues/new` URL)
note: creates the issue with the redacted title and body, or comments on `--comment <number>`; marks the record `filed` with the URL. Gated in every autonomy mode (see gates.md); the audit target is `<repo>: <title>`.

### rota bugreport dismiss
rota bugreport dismiss <id>
repo: none
data: {"id": string, "changed": true}
exit: 2, 3 as draft

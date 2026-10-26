---
skill: rota-bugreport
lane: maintenance
order: 95
situation: rota itself misbehaved
summary: "File rota's own mistakes upstream, after your explicit yes."
not_when:
  - use: rota-capture
    when: the bug is in your own project, not in rota
  - use: rota-debug
    when: you need to find the cause of a bug in your own code
before: []
after: []
manual_only: false
flags: []
example:
  source: null
  reviewed: false
  steps:
    - you: /rota-bugreport
    - agent: |
        1 open record: rota worker gate exited 70.
        Draft (redacted; rota version, OS, harness, model attached):
          title: worker gate exits 70 on a PR with no body
        Checked l4ci/rota for duplicates: none.
        File it?
    - you: "yes"
  changes:
    - an issue filed on the rota repo
    - the local record in .rota/bugreports.jsonl marked `filed`, with the issue URL
---
# /rota-bugreport

Files rota's own mistakes upstream. rota records them in `.rota/bugreports.jsonl` while it runs: a verb that fails with an internal error or panic (exit 70) writes a record, and an agent adds one it noticed with `rota bugreport add`. `rota round wind-down` and the Stop hook of a solo session print one non-blocking line when records are open. The skill lists them, shows each report redacted (tokens and keys, email addresses, home-directory paths, hostnames, your repo name and remote URL are removed) with the rota version, OS and architecture, harness and model, searches `l4ci/rota` for a duplicate (offering a comment instead), and files only after your yes for that report. Bugs in your own project stay with `/rota-capture`. Nothing is sent in the background.


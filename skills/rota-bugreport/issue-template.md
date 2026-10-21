# Issue template for a rota bug report

`rota bugreport draft` fills this in from the record. Section headings are fixed so reports read the same; the body under each is redacted before it is shown.

```markdown
## Summary

<one sentence: what went wrong>

## What happened

`rota <verb> <args>` failed with exit <code> (<n> times)

<error text, in a fenced block>

## Expected

<what rota should have done>

## Repro

1. <steps, or the command that failed>

## Environment

- rota: <version> (<commit>)
- OS/arch: <os>/<arch>
- harness: <claude|codex|opencode|…>
- model: <model>

## Redaction note

This report was redacted by `rota bugreport` before filing: tokens and keys, email addresses, home-directory paths, hostnames and the reporter's own repo name and remote URL were removed.
```

The title is `rota <verb>: <summary or first line of the error>`, at most 90 characters.

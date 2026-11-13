# No seam

Read from `SKILL.md` Step 7 when Step 5 found no seam for a regression test.

**No seam.** File the refactor item first (dedup and body shape as in `/rota-refactor`'s filing step), naming the missing seam and the bug it would have caught:

```bash
rota item create --json --kind tasks --title "<verb-first title naming the seam>" --desc "<one line>" --body-file <scratch> --related <ID>
rota issues label <number> --add refactor
```

The body needs `## Acceptance` boxes, one being a regression test for <ID> across the new seam. Check `rota tracker call -- issue list --label refactor --state all` for an open item on the same seam; if one exists, comment there instead of filing. File backend: `rota item create` alone. Then word the proof row to say no seam existed and link the item:

```bash
rota proof add <ID> --check "<reproducer command>" --result PASS --evidence "no regression test: no seam (<what would be mocked>); refactor #<n>" --sha <commit-hash>
```

Put the same line in the PR body.

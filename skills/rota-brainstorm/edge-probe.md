# Edge probe

Read from `SKILL.md` Step 3 when `brainstorm.edgeProbe` is `true`.

Read `rota config show brainstorm.edgeProbe`. `false` (default): skip this paragraph, brainstorm is unchanged. `true`: run the probe inside this one draft, with no extra approval step, and skip it when the issue body already has `## Acceptance`. Walk eight categories: boundary, empty, ordering, idempotency, concurrency, encoding, precision, adjacency. Answer each from the item and the code, and record it in the draft as `covered: <how>` or `dismissed: <reason>`. Then ask once, "What must this never do?", and write each answer as a `- [ ] AC-n:` line that states a prohibition ("never X"), under Acceptance.

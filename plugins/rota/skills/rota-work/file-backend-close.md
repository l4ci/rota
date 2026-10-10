# File-backend close (`/rota-work` Step 9)

Loaded by `SKILL.md` Step 9, file backend only.

Per resolved item (match by keyword overlap between task and item title; leave items you didn't work on), in this order:

1. **Tombstone the item's plan.** Skip when the item has no milestone tag or no plan file:

   ```bash
   MILESTONE=$(rota item field get <ID> --name milestone)
   if [ -n "$MILESTONE" ] && [ -f ".rota/plans/${MILESTONE}-<ID>.md" ]; then
     rota plan rm "${MILESTONE}-<ID>"
   fi
   ```

   **Slice plans (`M01-S01.md`) stay**: a slice covers several items; cleanup is manual via `rota plan rm <key>`.

2. **Complete the item:**

   ```bash
   rota item complete <ID> --commit <commit-hash>
   ```

3. **Commit by name.** No directory-wide `git add .rota/`:

   ```bash
   git add .rota/BACKLOG.md [.rota/plans/<key>.md …]
   git commit -m "chore: close <IDs>"
   ```

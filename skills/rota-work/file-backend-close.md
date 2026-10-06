# File-backend close (`/rota-work` Step 9)

Loaded by `skills/rota-work/SKILL.md` Step 9 on the file backend only.

**File backend**, per resolved item (match by keyword overlap between task and item title; leave items you didn't work on), in this order:

1. **Tombstone the item's plan.** Skip when the item has no milestone tag or no plan file:

   ```bash
   MILESTONE=$(rota item field get <ID> --name milestone)
   if [ -n "$MILESTONE" ] && [ -f ".rota/plans/${MILESTONE}-<ID>.md" ]; then
     rota plan rm "${MILESTONE}-<ID>"
   fi
   ```

   The plan's decomposition is stale once the item ships; truth lives in code and commits. **Slice plans (`M01-S01.md`) stay**: a slice covers several items, and cleanup is manual via `rota plan rm <key>`.

2. **Complete the item:**

   ```bash
   rota item complete <ID> --commit <commit-hash>
   ```

   `rota item complete` is the only write to the backlog; never edit `.rota/` by hand.

3. **Commit by name.** Step 1 may have removed a plan file and step 2 leaves `.rota/BACKLOG.md` modified. No directory-wide `git add .rota/`:

   ```bash
   git add .rota/BACKLOG.md [.rota/plans/<key>.md …]
   git commit -m "chore: close <IDs>"
   ```

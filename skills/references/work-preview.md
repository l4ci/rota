# Preview mode (`/rota-work --preview <target>`)

Read-only approach peek, then stop. No writes, no commits, no guard, no status registration, no `rota` calls beyond reads. Steps 1–11 of `/rota-work` are bypassed.

The target is a backlog item (`#42`, or `B07`/`F03`/`T11` on the file backend), a plan key (`M01-S01`, `M01-B07`) or a milestone (`M01`). Ambiguous: ask once, don't auto-pick.

## Procedure

1. **Load context silently** per [`context-load-protocol.md`](context-load-protocol.md), all reads in parallel. For an item under umbrella mode (`rota repo umbrella` exits 0) with a `Repos:` value (`rota item field get <ID> --name repos`), resolve it with `rota repo resolve <name>… --json` and keep every entry. Skip repo resolution for slice and milestone targets. Matches from `rota decisions query` go in the peek's "Hard boundaries to respect" section, one line each, so the user can spot conflicts before code lands.
2. **Print the peek and nothing else** (no preamble, no recap of what you read):

   ```
   Peek for <target>:

   Approach
     <one paragraph: the shape of what I'd do and why this over alternatives>

   Repo
     <name> (<absolute-sub-repo-path>)        # omit when single-repo or no Repos: tag
     <name2> (<absolute-sub-repo-path-2>)     # one line per repo for multi-repo items

   Files I'd touch
     - <path>  — <reason>

   Files I'd create
     - <path>  — <reason>

   Hard boundaries to respect
     - <decision title>  — <one-line summary>

   Tests I'd add
     - <test name or location>  — <what it verifies>

   Assumptions I'm making
     - <named assumption that, if wrong, changes the approach>

   Known unknowns
     - <thing I'd resolve mid-flight>  (will pause if unresolvable)

   If any of this is wrong, push back before /rota-work runs.
   ```

   Omit `Hard boundaries to respect` when no DECISIONS topic matched. Omit `Repo` when umbrella mode is off, the target is a slice or milestone, or the item has no `Repos:` tag. Be specific: *"I'd touch the auth code"* is useless, so cite paths, test names and function names. A path you can't cite goes under Known unknowns.
3. **Stop.** Don't invoke `/rota-work` without `--preview`, write a plan or act. The user says "go" (and runs `/rota-work <target>` themselves), pushes back (restate the peek with corrections) or asks for a written plan (offer `/rota-plan <target>`; a plan beats a peek for high-stakes work).

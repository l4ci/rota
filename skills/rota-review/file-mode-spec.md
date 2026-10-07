# File-mode spec (Step 3)

When `backlog.backend` is `"file"`, collect the spec per `referencedId` like this instead of the issue-mode flow:

- The item's `Intent` line from `intents`, plus its plan when it has a milestone (`rota item field get <ID> --name milestone`, then `rota plan show "<MNN>-<ID>"`; exit 3 means no plan file). Lift the plan's `## Review Focus` section into the brief verbatim, and its `## Relies on` list for Step 4.

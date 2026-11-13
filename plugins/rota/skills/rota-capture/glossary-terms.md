# Step 6 — Consult the Glossary

Loaded by `SKILL.md` Step 6 when the user's phrasing may map to a glossary term.

Scan the `## Glossary` topic of `.rota/KNOWLEDGE.md` (`rota glossary read <term>`). If the user's phrasing maps to a canonical term or alias, write the canonical name silently (no question) and add one line to the report: `Used canonical term "<term>" for "<phrasing>".` Spend a question only when one phrase maps to two glossary entries; ask which, with the likelier entry `(Recommended)`, and count it against the Step 3 cap. If the capture introduces a new domain concept the user names, suggest `/rota-learn --term <name>` afterwards; never auto-invoke.

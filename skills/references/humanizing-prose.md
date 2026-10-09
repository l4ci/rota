# Humanizing user-facing prose

Used by `/rota-release` Step 5 (release notes + summary line), `/rota-ship` Step 4 (PR body), and `/rota-ship` Docs Mode Step D-A4 (doc-page edits). Defines the rule sheet and self-audit pass that run after drafting user-facing prose, before the user sees it.

The general AI-tells catalog (puffery, promotional adjectives, -ing clauses, copula avoidance, negative parallelism, rule-of-three, synonym cycling, AI vocabulary, filler, hedging) is not repeated here. See Wikipedia's [Signs of AI writing](https://en.wikipedia.org/wiki/Wikipedia:Signs_of_AI_writing) or the `humanizer` skill. The rules below are project-specific.

## Voice for rota artifacts

- **Evidence over assertion.** Cite the change, not its importance. *"Adds `--remove` flag to `/rota-capture`"* beats *"a powerful new capability for backlog management"*.
- **Terse.** Sentences earn their length. Cut filler.
- **No slogans.** Generic upbeat closers are dead weight.
- **First-person plural for project intent**, never for individual actions. *"We removed eight commands"* is fine; *"we ran the smoke test"* isn't.
- **One voice per artifact.** Release notes, PR bodies, and docs each have a target reader; a draft should not switch register mid-paragraph.

## House style

- **Em dashes.** rota uses them deliberately. At most one per sentence; never two in a row to wrap a parenthetical when commas would do. Don't strip them blanket.
- **Sentence-case headings.** `## Strategic negotiations and global partnerships`, not title case.

## Self-audit pass

After drafting any user-facing prose, before showing it to the user:

1. Re-read the draft once with the voice and house-style rules above and the AI-tells catalog linked at the top in mind.
2. Answer one question internally: *"What in this draft would make a careful reader assume an LLM wrote it?"*
3. Revise the specific tells you named in step 2. Replace them rather than rewriting around them.
4. Show the revised draft to the user.

The self-audit is silent. The user sees one draft, the post-audit one. Don't narrate the audit or list the patterns you removed.

## What this reference does NOT cover

- **Commit messages.** Commit subjects and bodies follow the project's existing `git log` style and stay terse by construction; they are not user-facing artifacts.
- **Question option text.** Each skill writes its own prompts; this reference is for generated artifacts, not UX prompts.
- **KNOWLEDGE.md / DECISIONS.md content.** Their structure is encoded in `references/persistence-skills.md`. The prose rules above apply to the bullet text but the structural shape stays as defined there.

# Humanizing user-facing prose

Used by `/rota-release` Step 5 (release notes + summary line), `/rota-ship` Step 4 (PR body), and `/rota-ship` Docs Mode Step D-A4 (doc-page edits). Defines the rule sheet and self-audit pass that runs after the model drafts user-facing prose, before the draft is shown to the user.

The patterns are distilled from Wikipedia's [Signs of AI writing](https://en.wikipedia.org/wiki/Wikipedia:Signs_of_AI_writing). The voice rules are project-specific: pinned here so they ship with rota and apply consistently across releases, PRs, and docs without depending on any external skill being installed.

## Voice for rota artifacts

- **Evidence over assertion.** Cite the change, not its importance. *"Adds `--remove` flag to `/rota-capture`"* beats *"a powerful new capability for backlog management"*.
- **Terse.** Sentences earn their length. Cut filler.
- **No slogans.** Generic upbeat closers are dead weight; the reader didn't ask for a pep talk.
- **First-person plural for project intent**, never for individual actions. *"We removed eight commands"* is fine; *"we ran the smoke test"* isn't.
- **One voice per artifact.** Release notes, PR bodies, and docs each have a target reader; the rules below tighten all three, but a single draft should not switch register mid-paragraph.

## Patterns to cut

Each pattern lists what to remove and what to write instead. Roughly highest-payoff to lowest, but every entry is in scope.

### Significance / legacy puffery
Cut: *"marks a pivotal moment"*, *"stands as a testament to"*, *"represents a shift in"*, *"key turning point"*, *"setting the stage for"*, *"underscores the importance of"*.
Why: release notes and docs are factual records. Significance is the reader's call.

### Promotional adjectives
Cut: *"groundbreaking"*, *"powerful"*, *"robust"*, *"comprehensive"*, *"vibrant"*, *"seamless"*, *"intuitive"*, *"breathtaking"*, *"must-have"*.
Use: the verb + the concrete thing. *"Adds X"*, *"removes Y"*, *"the helper now returns Z"*.

### Superficial -ing clauses
Cut: trailing *"...enabling X, fostering Y, contributing to Z"*. These tack fake depth onto sentences.
Use: a separate sentence with a concrete claim, or omit.

### Copula avoidance
Cut: *"serves as"*, *"functions as"*, *"stands as"*, *"acts as"*, *"boasts"*, *"features"* (as the main verb).
Use: *"is"* / *"are"* / *"has"*.

### Negative parallelism
Cut: *"It's not just X, it's Y"*, *"More than a feature — a contract"*, *"Not merely Z, but Q"*.
Use: the load-bearing claim alone, or both claims as separate sentences.

### Rule-of-three padding
Cut: three-item lists where one or two items are real and the third is filler (*"speed, quality, and adoption"*, *"plan, build, ship"* when there's actually a fourth step).
Use: the items that carry weight. Two is fine. Four is fine.

### Elegant variation (synonym cycling)
Cut: *"The orchestrator dispatches workers. The conductor assigns tasks. The coordinator..."* — same noun, three synonyms.
Use: the same noun every time. Repetition is clearer than variety here.

### AI-vocabulary words
Cut: *additionally*, *crucial*, *delve*, *underscore* (verb), *highlight* (verb), *interplay*, *intricate*, *pivotal*, *showcase*, *tapestry*, *testament*, *vibrant*, *enduring*, *garner*, *align with*, *foster*, *leverage* (verb).
Use: plain alternatives. *Also*, *important*, *explain*, *show*, *complex*, *use*.

### Em-dash overuse
rota uses em dashes deliberately and often. The rule: at most one per sentence; never two in a row to wrap a parenthetical when commas would do. Don't strip em dashes blanket — the project voice uses them.

### Boldface and inline-header bullet lists
Cut: every bullet starting with **Bold Phrase:** followed by a sentence. The boldface mimics importance without earning it.
Use: prose paragraphs, plain bullets, or a real table when the data is tabular.

### Title case in headings
Cut: `## Strategic Negotiations And Global Partnerships`
Use: `## Strategic negotiations and global partnerships` — sentence case throughout rota artifacts.

### Sycophantic closers / generic positive conclusions
Cut: *"This represents an exciting step forward"*, *"the future looks bright"*, *"watch this space"*, *"stay tuned"*.
Use: the next concrete action, or nothing.

### Filler phrases
Cut → Use:
- *"in order to"* → *"to"*
- *"at this point in time"* → *"now"*
- *"due to the fact that"* → *"because"*
- *"it is important to note that"* → omit; just state the thing
- *"the ability to X"* → *"can X"*

### Excessive hedging
Cut: *"could potentially possibly"*, *"might have some effect"*, *"it could be argued that"*.
Use: the claim, or omit. If genuinely uncertain, *"may"* is enough.

## Self-audit pass

After drafting any user-facing prose, before showing it to the user:

1. Re-read the draft once with the patterns above in mind.
2. Answer one question internally: *"What in this draft would make a careful reader assume an LLM wrote it?"*
3. Revise the specific tells you named in step 2. Replace them rather than rewriting around them.
4. Show the revised draft to the user.

The self-audit is silent. The user sees one draft — the post-audit one. Don't narrate the audit; don't list the patterns you removed.

## What this reference does NOT cover

- **Commit messages.** Commit subjects and bodies follow the project's existing `git log` style and stay terse by construction; they're not user-facing artifacts in the same sense.
- **AskUserQuestion option text.** Each skill writes its own prompts; this reference is for generated artifacts, not UX prompts.
- **KNOWLEDGE.md / DECISIONS.md content.** Their structure is encoded in `references/persistence-skills.md`. The prose rules above apply to the bullet text but the structural shape stays as defined there.

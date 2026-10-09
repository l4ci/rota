---
verified-sha: c535c4cf9bd5b3b92d47e1d1df0c0014b4bfe750
refs:
  - internal/gate
  - internal/cli/gate.go
---

## B1: manual gates

B1 (#54) moves the manual gates out of skill prose and into the verbs. Orchestrator rulings (round 4, phase B, from the maintainer on 2026-10-03): a gated verb exits non-zero without `--confirm` at every autonomy level; skills pass `--confirm` only after an `AskUserQuestion` yes; every confirmed pass is appended to an audit log under `.rota/` with the gate, the verb, the time and the quoted human answer (`--confirm-note`, required with `--confirm`). Issue close and issue label are not gated in code. Gated: tag push, release publish, public filing (`tracker suggest-upstream`), and merges where config requires human approval (all merges, or merges touching listed paths, per #44 "Merging").

- **Registry.** One table in `internal/gate` names every manual gate the skills describe, enforced or not. `rota gate list` prints it. A gate is `enforced` when a verb refuses without `--confirm`; the rest stay skill judgment (an `AskUserQuestion` the skill must not auto-pick) and are listed so the inventory is complete in one place.

| Gate | Enforced by | State it creates |
|---|---|---|
| `tag-push` | `release push` | the release tag (and branch) on the remote |
| `release-publish` | `release publish` | a GitHub or GitLab release page |
| `public-filing` | `tracker suggest-upstream` | a public issue on the hv-skills repo |
| `merge-approval` | `ship merge`, `ship pr-merge`, `worker gate` (only when `ship.mergeApproval` applies) | a merge into the base branch |
| `debug-reset` | `debug reset` (B3) | a fresh failed-fix count for an item the Iron Law halted |
| `pr-open` | skill only (`/rota-ship`) | a public PR or MR |
| `issue-close` | skill only (`/rota-ship`, `/rota-release`); ruled out of code | closed upstream issues |
| `decision-write` | skill only (`/rota-decide`) | a hard boundary in `DECISIONS.md` |

- **Confirmation flags.** Every enforced verb takes `--confirm` and `--confirm-note <answer>`. `--confirm-note` holds the human's answer, quoted as given. `--confirm` without a non-empty `--confirm-note`, or `--confirm-note` without `--confirm`, is exit 2. `autonomy.level` is never read to clear a gate: `off` and `auto` both refuse alike.
- **Refusal.** A gated verb that would cross its gate without `--confirm` exits 4 before it changes anything, with failure data `{"blockedBy": "manual gate", "gate": string, "changed": false}`; `merge-approval` adds `"paths": []string`, the changed paths that matched `ship.mergeApprovalPaths` (empty under `all`). The hint names the flags to pass after asking. The check runs after usage and resolution checks, so a bad call still exits 2 or 3 and a refused one tells the caller exactly what needs approval.
- **Audit.** A cleared gate appends one JSON line to `.rota/gate-audit.jsonl` at the project root, under the `.lock` sidecar, before the gated action runs: `{"ts": string, "gate": string, "verb": string, "target": string, "note": string, "autonomy": string, "escalation"?: string}`. `escalation` is set when the approval came through `--approval` (C5). `ts` is RFC 3339 UTC; `target` is the tag, branch, PR or issue title the verb acts on; `autonomy` is the `autonomy.level` in force, recorded for evidence only. The line is written even when the action then fails, because the human did approve that attempt. The file is per-developer runtime state and is gitignored (`rota init` adds `.rota/gate-audit.jsonl` to the managed block): a tracked log would dirty the base branch on every merge or release push. `--confirm` on a call that crosses no gate (a merge `ship.mergeApproval` does not cover) is accepted and writes nothing.
- **Merge approval config.** `ship.mergeApproval` is `none` (default; the orchestrator merges after the gate passes, per #44), `all` (every merge needs `--confirm`) or `paths` (only merges whose changed files match `ship.mergeApprovalPaths`). `ship.mergeApprovalPaths` is a list of repo-relative entries; a changed file matches an entry when it equals it, lies under it (`skills/rota-release` matches `skills/rota-release/SKILL.md`), or matches it as a `path.Match` glob against the whole path (`*.md` matches top-level files only). Changed files are `git diff --name-only <base>...<branch>` for `ship merge` and `worker gate`, and the PR's file list from the forge for `ship pr-merge`. Neither key is written by `rota init`. Any other `ship.mergeApproval` value is exit 2 on a merge verb, never a silent `none`.
- **Amended entries.** `tracker suggest-upstream`, `ship merge`, `ship pr-merge` and `worker gate` take the confirmation flags and gain the exit-4 refusal; their entries above say where the check runs. `worker gate` is a check verb (rule 7) but exits 4 on this refusal, not 1, because it refuses before it has judged anything: the merge has not been attempted, and `data.verdict` is `approval-required`.

### rota gate list
rota gate list
repo: none
data: {"gates": [{"name": string, "enforced": bool, "verbs": []string, "skills": []string, "creates": string}]}
exit: implied only
old: none (new in B1)
note: the registry above, in that order. `verbs` are verb paths (`"release push"`), empty for skill-only gates; `skills` are the skill names whose text routes through the gate (`"rota-release"`). Runs without a `.rota/` root.

### rota release push
rota release push <X.Y.Z> [--branch <name>] [--tag-only | --branch-only] --confirm --confirm-note <answer>
repo: scoped
data: {"tag": string, "branch": string, "remote": string, "scope": "both"|"tag"|"branch", "changed": bool}
exit: 2 when the version is not bare X.Y.Z, a confirmation flag is given without the other, or both of `--tag-only` and `--branch-only` are given; 3 when tag `v<X.Y.Z>` does not exist locally, the branch does not exist, there is no `origin` remote, `--branch-only` finds the tag missing on `origin`, or (GitHub) finds its release missing or still a draft; 4 when the checkout has a `.goreleaser.yaml` or `.goreleaser.yml` and neither `--tag-only` nor `--branch-only` is given (failure `data` `{"blockedBy": "release order", "changed": false}`); 4 when the `tag-push` gate is not cleared; 5 when `git push` fails
old: none; replaces the raw `git push origin <current-branch> v<X.Y.Z>` in `/rota-release` Step 12
note: by default one `git push origin <branch> v<X.Y.Z>`, so the commit and the tag land together as before. F4 (#74) splits it for the release order: `--tag-only` pushes the tag (the release workflow builds from it), and `--branch-only` pushes the branch once the tag is on `origin`, so the plugin version on the branch never leads the binaries it names. Both use the same `tag-push` gate and target. Where goreleaser builds the release, an unflagged push is refused, since it would put the plugin version on the branch before the binaries exist. `--branch-only` on GitHub also runs `gh release view` and needs a published (non-draft) release. Both checks run before the gate, so a refusal does not use up the approval. `--branch` defaults to the current branch; a detached HEAD without `--branch` is exit 3. `remote` is always `origin`. On exit 5 `error.message` carries the tag SHA for manual recovery.

### rota release publish
rota release publish <X.Y.Z> --title <text> --body-file <path|-> [--draft] --confirm --confirm-note <answer>
repo: scoped
data: {"tag": string, "host": string, "url": string, "draft": bool, "changed": bool}
exit: 2 when the version is not bare X.Y.Z, --title or --body-file is missing or the body is empty, or a confirmation flag is given without the other; 3 when tag `v<X.Y.Z>` does not exist on `origin`, when GitHub has a draft release for it that lacks any of `rota_linux_amd64`, `rota_linux_arm64`, `rota_darwin_amd64`, `rota_darwin_arm64` or `checksums.txt` (the tarballs do not count), or when it has no release and the checkout has a `.goreleaser.yaml` or `.goreleaser.yml`; 4 when the `release-publish` gate is not cleared; 5 when `gh` or `glab` is missing, unauthenticated or fails
old: none; replaces the host blocks of `references/release-hosts.md` in `/rota-release` Step 13
note: the host is `rota release host`. `github` and `github-enterprise` run `gh release create v<X.Y.Z> --title <text> --notes-file <file> [--draft]`; `gitlab` and `gitlab-self-hosted` run `glab release create v<X.Y.Z> --name <text> --notes-file <file>` (`--draft` is a usage error there, exit 2, because GitLab has no draft releases). `url` is the last stdout line of the CLI.
note: one release per version (F4, #74). On GitHub the verb first runs `gh release view v<X.Y.Z> --json isDraft,assets`. An existing release (the draft the release workflow builds) is finished with `gh release edit v<X.Y.Z> --title <text> --notes-file <file> --draft=<bool>`; a missing one is created as above, unless goreleaser builds this repo, in which case the workflow has not run and the verb exits 3 rather than put the release ahead of its binaries. A `view` failure other than "release not found" is exit 5. The view and its exit-3 checks run before the `release-publish` gate, so waiting for the workflow does not use up the approval. `gh release view` finds drafts from gh 2.45 on (it falls back to a GraphQL lookup by pending tag; read in cli/cli at v2.45.0, not exercised against GitHub here). GitLab is unchanged.
note: host `none` publishes nothing and needs no approval: exit 0, `changed: false`, `url` `""`, and the warning `no recognized remote; nothing published`. The gate check runs after the host is known, so a `none` host never refuses.

# Package check

Used by `/rota-spike` Step 2 and `/rota-plan` Step 2 when `rota config show research.packageCheck` is `true`. With `false` (the default) neither skill makes a registry call and this file is not read. The check makes network calls, which is why it is opt-in.

## Scope

Check each **new** package the skill would recommend: one the repo does not already depend on. A package already in the lockfile or manifest is not checked, and this is not a vulnerability scan. It is a guard against a name that does not exist or was registered just now.

## Lookup

One GET request, nothing written, against the public registry for the ecosystem. Send a `User-Agent` (crates.io requires one). A network failure is not a verdict: say the lookup failed, and treat the package as `suspect` with reason `lookup failed`.

| Ecosystem | URL | First published | Source repository link |
|-----------|-----|-----------------|------------------------|
| npm | `https://registry.npmjs.org/<name>` | `time.created` | `repository` |
| PyPI | `https://pypi.org/pypi/<name>/json` | earliest `upload_time` across `releases` | a `Source`, `Repository` or `Homepage` entry in `info.project_urls` |
| crates.io | `https://crates.io/api/v1/crates/<name>` | `crate.created_at` | `crate.repository` |
| Go proxy | `https://proxy.golang.org/<module>/@v/list`, then `<module>/@v/<earliest>.info` | `Time` of the earliest version | the module path itself when it starts with a code host (`github.com/`, `gitlab.com/`, `codeberg.org/`); otherwise none |

HTTP 404 (Go proxy: 404 or 410) means the name is not on the registry.

## Verdicts

- `missing`: the name is not on the registry.
- `suspect`: on the registry, and first published under 90 days ago, or no source repository link. Name every reason that applies.
- `ok`: on the registry, first published 90 days ago or more, with a source repository link.

## What the skill does with a verdict

- `missing`: do not recommend the package. Say it was dropped and why. Pick another or recommend none.
- `suspect`: list it with its reason(s) and ask the user once whether to keep it. It appears in the spike finding or the plan only after the user says yes. A "no" drops it like a `missing` one.
- `ok`: recommend as normal. Say nothing extra.

Record the outcome in one line per package (`<name> (<ecosystem>): ok|suspect: <reason>|missing`) so the user sees what was checked.

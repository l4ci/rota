# Publish and push failures (Steps 10, 11, 11b)

- **Step 10, tag push.** Exit 3 (no origin) or 5 (push failed): stop; the error names the tag SHA for manual recovery. With a `.goreleaser.yaml`, the tag starts the workflow that builds binaries into a draft release.
- **Step 11, publish.** There is one release per version: where the workflow already made a draft, the verb finishes it (notes, title, un-draft) and never creates a second. It exits 3 while that draft lacks any of the four `rota_<os>_<arch>` binaries or `checksums.txt`, lacks the `.minisig` of any attached asset (binaries, tarballs, `checksums.txt.minisig`; `install.sh` refuses an unsigned binary, see `docs/contributing/release-signing.md`), or while no release exists and the repo builds with goreleaser, so wait for the workflow (`gh run watch`) and re-run. Add `--draft` when `release.draft` is true and the host is GitHub (GitLab refuses it). Origin on neither host: the verb publishes nothing (`changed: false`) and the summary says `skipped`. Exit 5 (`gh`/`glab` missing): print the error and continue; the tag is already public.
- **Step 11b, branch push.** Exits 3 while the tag is not on origin or its release is missing or still a draft, so the branch never leads the binaries.

## Edge cases

- **Multiple version files** — first match wins; pin with `release.versionFile`.
- **`gh`/`glab` missing but origin matches** — Step 11 fails after the tag push, so the branch is still unpushed. Recovery: install the CLI and re-run `rota release publish <X.Y.Z> --title … --body-file <path> --confirm --confirm-note "<answer>"`; to revert the tag, `git push --delete origin v<X.Y.Z>`.
- **No origin** — push exits 3; publish is skipped. Tag and CHANGELOG stay committed locally.

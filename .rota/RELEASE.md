# Release Checklist

Each `- [ ]` line is a gate `/rota-release` walks before bumping the version. Edit freely — nothing here is hardcoded. Items marked `- [x]` are ignored. Append `(manual)` to any item that must interject even in `autonomy.level: auto`/`loop`.

- [ ] `VERSION` holds the new version and the CHANGELOG heading matches it (the tag, `v<VERSION>`, is what builds the binary; `release.yml` refuses a mismatch)
- [ ] `python3 test/validate-skills.py` passes
- [ ] `HOMEBREW_TAP_DEPLOY_KEY=x goreleaser release --snapshot --clean --skip=sign` builds `rota_<os>_<arch>` for linux and darwin on amd64 and arm64, plus `checksums.txt` and the `rota` formula (asset names are the contract with `install.sh`)
- [ ] The Homebrew tap `l4ci/homebrew-tap` exists and has the public half of an SSH deploy key with write access, and the private half (no passphrase) is the secret `HOMEBREW_TAP_DEPLOY_KEY` on `l4ci/rota`; a local snapshot run needs the variable set to any value (`HOMEBREW_TAP_DEPLOY_KEY=x`), since goreleaser renders the template before it skips the push (manual)
- [ ] The Actions secret `MINISIGN_SECRET_KEY` holds the unencrypted minisign key whose public half is `ROTA_MINISIGN_PUBKEY` in `install.sh`; the release workflow signs every asset and uploads the `.minisig` files, and `install.sh` refuses an unsigned or mis-signed binary. Generating or rotating the key: `docs/contributing/release-signing.md` (manual)
- [ ] GitHub Actions is enabled on `l4ci/rota`, so the `v*` tag runs `.github/workflows/release.yml` (manual)
- [ ] After the workflow finishes (`gh run watch`), the draft lists a `.minisig` next to each `rota_<os>_<arch>` (`gh release view v<VERSION> --json assets`); then run `rota release publish` right away: the formula points at assets that only resolve once the draft is published (manual)
- [ ] CLAUDE.md template managed blocks reflect any new query helpers or topic indexes
- [ ] `bash test/smoke.sh` is green on this branch
- [ ] CHANGELOG.md entry for the version is human-readable — bullets compressed, themes named, no raw commit dumps

(Add release-cycle-specific items below as they come up; trim entries that stop being load-bearing.)

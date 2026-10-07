# Release signing

Release assets are signed with [minisign](https://jedisct1.github.io/minisign/). `install.sh` verifies `rota_<os>_<arch>` against `rota_<os>_<arch>.minisig` using the public key embedded in the script, then checks the sha256 as before. Without a valid signature nothing is installed.

The release workflow (`.github/workflows/release.yml`) signs every uploaded file (binaries, tarballs, `checksums.txt`) through the `signs:` block in `.goreleaser.yaml`. It reads the key from the Actions secret `MINISIGN_SECRET_KEY`.

Current key id: `2153154F7AA18B5D`. The public key is the `ROTA_MINISIGN_PUBKEY` line at the top of `install.sh`.

## Generate or rotate the key

The key is unencrypted, so CI signs without a password secret. Generate it with the same implementation CI uses (`aead.dev/minisign` v0.3.0; jedisct1/minisign output is compatible):

```bash
go install aead.dev/minisign/cmd/minisign@v0.3.0
minisign -G -W -p rota-release.pub -s rota-release.key
```

Then:

1. Set the secret to the full contents of the key file: `gh secret set MINISIGN_SECRET_KEY --repo l4ci/rota < rota-release.key`.
2. Put the second line of `rota-release.pub` (the base64 key, starting `RW`) into `ROTA_MINISIGN_PUBKEY` in `install.sh`, and update the key id in the comment above it and in this file.
3. Store the key file in your password manager, then delete the local copy.

Rotating is the same. Order matters: `install.sh` on `main` is what users run, so merge the new public key and cut a release signed with the new key together. A release signed with the old key stops verifying for anyone who fetches the new `install.sh`, and the reverse. Re-sign or re-release the versions people still pin with `--version` if they must stay installable. If the old key leaked, say so in the release notes.

## ✅ Verify by hand

```bash
minisign -V -P RWRdi6F6TxVTIW92f3/QsWBl5VHdXm1FABgexyAla0z3A5WT4JzG6/SP \
  -m rota_linux_amd64 -x rota_linux_amd64.minisig
```

## Test locally

A local snapshot has no key; skip signing:

```bash
HOMEBREW_TAP_DEPLOY_KEY=x goreleaser release --snapshot --clean --skip=sign
```

To exercise signing, generate a throwaway key as above and run goreleaser with `MINISIGN_KEY_FILE=<keyfile>` instead of `--skip=sign`. `bash test/smoke.sh` section 97 covers `install.sh` against fake releases signed with a throwaway key; it needs `minisign` on `PATH`.

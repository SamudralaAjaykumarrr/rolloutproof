# Releasing RolloutProof

## Versioning

RolloutProof follows [Semantic Versioning](https://semver.org/): tags
are `vMAJOR.MINOR.PATCH` (e.g. `v0.1.0`). Until `v1.0.0`, the invariant
catalog and CLI surface can still change in ways `docs/invariants.md`'s
own ID-stability rule doesn't cover (an ID's *meaning* is stable once
shipped; the surrounding CLI/output format is not yet).

## Before tagging a release

```bash
gofmt -l .                    # must print nothing
go vet ./...
go build ./...
go test ./...
go test -race ./...
go run ./cmd/eval              # 100% of the scenario corpus must pass
git status --short             # must be empty (clean tree)
```

This is the same gate `.github/workflows/ci.yml` runs on every push —
if `main` is green there, it's release-ready by this project's own bar.

Update `CHANGELOG.md`: move the relevant `[Unreleased]` entries under a
new `## [vX.Y.Z] - YYYY-MM-DD` heading, in the same Added/Fixed/Changed
grouping already used there.

## Tagging

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

Pushing a tag matching `v*` triggers `.github/workflows/release.yml`,
which:

1. Re-runs the full test gate above (a release is never built from
   untested code, even if the tag was cut from a green `main`).
2. Cross-compiles `cmd/rolloutproof` for `linux/amd64`, `linux/arm64`,
   `darwin/amd64`, `darwin/arm64`, and `windows/amd64`, each with real
   version metadata embedded via `-ldflags -X` (`internal/version`) —
   the same flags documented in `README.md`'s install section, not a
   separate, undocumented mechanism.
3. Computes a `SHA256SUMS` file over all built archives.
4. Publishes a GitHub Release for the tag with the binaries and
   checksums attached, and release notes pulled from that version's
   `CHANGELOG.md` section.

## Verifying a release

After the workflow completes:

```bash
curl -sL https://github.com/SamudralaAjaykumarrr/rolloutproof/releases/download/vX.Y.Z/rolloutproof_vX.Y.Z_linux_amd64.tar.gz | tar xz
./rolloutproof version    # should print vX.Y.Z, the release's own commit, and build date
sha256sum -c SHA256SUMS   # verify the archive against the published checksums
```

`go install github.com/SamudralaAjaykumarrr/rolloutproof/cmd/rolloutproof@vX.Y.Z`
also works for any tagged version — Go's module proxy serves it
directly from the tag, no release workflow involvement needed — but it
embeds only what `go build` embeds by default (module version, no
`Commit`/`Date` — see `internal/version`'s own doc comment), which is
why the workflow above additionally publishes binaries built with
explicit `-ldflags`.

## What a release must never include

- No secrets, tokens, or credentials in the built archives or the
  workflow's own logs (the workflow uses only the ambient
  `GITHUB_TOKEN` GitHub provides per run, scoped to `contents: write`
  for that job alone).
- No generated/build artifacts committed to the repository itself —
  release binaries are workflow outputs attached to the GitHub Release,
  never checked into git.

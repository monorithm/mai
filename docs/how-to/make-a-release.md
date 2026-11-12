# Make a release

Use this procedure to publish a new version of mai.

## Procedure

1. Make sure that CI passes on `main`.
2. Set the new version:

   ```sh
   uv version X.Y.Z
   ```

3. In `CHANGELOG.md`, move the lines of `[Unreleased]` to a new section
   `[X.Y.Z] - YYYY-MM-DD`.
4. Commit the change with the message `chore(release): vX.Y.Z`.
5. Make the tag and push it:

   ```sh
   git tag vX.Y.Z
   git push origin main vX.Y.Z
   ```

The workflow `release.yml` runs CI, builds the packages, pushes the image
and publishes the release. Then GoReleaser on macOS builds the six binaries
of the host CLI and adds them and their checksums to the release.

## The binaries of the CLI

The file `.goreleaser.yaml` gives the release of the CLI:

| Item | Value |
|---|---|
| Binaries | macOS, Linux, and Windows, each `amd64` and `arm64` |
| cgo | On for macOS, off for Linux and Windows |
| Version | `-X github.com/monorithm/mai/cli/internal/version.Version` from the tag |
| Signature | An ad-hoc signature with the hardened runtime on macOS |
| Packages | A Homebrew formula in `monorithm/homebrew-tap` and a Scoop manifest in `monorithm/scoop-bucket` |

For the release, the repository secrets `HOMEBREW_TAP_TOKEN` and
`SCOOP_BUCKET_TOKEN` are necessary. If a token is missing, GoReleaser writes the formula or
the manifest to `dist/` and does not upload it.

To make a test build with no upload, run this command on macOS:

```sh
goreleaser release --snapshot --clean
```

> [!NOTE]
> A Developer ID signature and notarization are not part of the release. After
> each upgrade, macOS shows one keychain prompt for the new binary.

## Image tags

The release pushes the image `ghcr.io/monorithm/mai` for `linux/amd64` and
`linux/arm64`. The repository and the image are public. For a pull, a login
and a token are not necessary. For the tag `vX.Y.Z`, the image gets these tags:

| Tag | Example for `v2.0.0` | Changes |
|---|---|---|
| `X.Y.Z` | `2.0.0` | Never. |
| `X.Y` | `2.0` | At each release of `X.Y`. |
| `X` | `2` | At each release of `X`. |

The release does not change the tag `latest`. The workflow `image.yml` sets
`latest` and `sha-<short>` at each push to `main`. A manual run of
`image.yml` pushes `sha-<short>` and the name of the branch. A pull request
builds the image, but it does not push it.

The CLI pulls the image with the same version as the CLI. Thus the CLI
`X.Y.Z` uses the image `X.Y.Z`.

After the release, make sure that a pull operates without a login. Use a
computer that has no login to `ghcr.io`:

```sh
docker pull ghcr.io/monorithm/mai:X.Y.Z
```

> [!CAUTION]
> Do not use a tag two times. If a release does not complete after the push, make a new
> version.

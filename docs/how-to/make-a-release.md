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
5. Make the two tags on the same commit, and push them:

   ```sh
   git tag -a vX.Y.Z -m "mai X.Y.Z"
   git tag -a cli/vX.Y.Z -m "mai CLI X.Y.Z"
   git push origin vX.Y.Z cli/vX.Y.Z
   ```

The tag `vX.Y.Z` starts the workflow `release.yml`. The workflow runs CI,
builds the packages, pushes the image, and publishes the release. The tag
`cli/vX.Y.Z` releases the Go module of the CLI. It starts no workflow.

## The Go module of the CLI

The CLI is a Go module in the folder `cli`. Users install it with
`go install`. The release has no binaries and no packages at this time.

| Item | Value |
|---|---|
| Module path | `github.com/monorithm/mai/cli/v3` |
| Tag | `cli/vX.Y.Z`. Go finds the version of a module in a folder by the tag with the folder as a prefix. |
| Install | `go install github.com/monorithm/mai/cli/v3/cmd/mai@vX.Y.Z` |
| Version | The CLI reads `vX.Y.Z` from the build information of Go. A local build shows a version that ends in `-dev`. |
| cgo | On macOS, the keychain and the presence check need cgo, thus a C compiler. The builds for Linux and Windows use no C compiler. |
| Signature | None. On Apple silicon, the linker gives each binary an ad-hoc signature. |

After the push of the tags, make sure that the Go module proxy has the
version:

```sh
GOPROXY=https://proxy.golang.org go list -m github.com/monorithm/mai/cli/v3@vX.Y.Z
```

> [!NOTE]
> The file `.goreleaser.yaml` stays in the repository. A subsequent release can
> use it for binaries, a Homebrew formula, and a Scoop manifest. The release
> workflow does not run it at this time.

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

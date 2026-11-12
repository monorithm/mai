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
and publishes the release.

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

To use a release, set `MAI_TAG`. Then pull the image and start the
container:

```sh
MAI_TAG=2.0.0 docker compose -f ~/.mai/bin/compose.yaml pull
MAI_TAG=2.0.0 docker compose -f ~/.mai/bin/compose.yaml up -d
```

After the release, make sure that a pull operates without a login. Use a
computer that has no login to `ghcr.io`:

```sh
docker pull ghcr.io/monorithm/mai:X.Y.Z
```

> [!CAUTION]
> Do not use a tag two times. If a release does not complete after the push, make a new
> version.

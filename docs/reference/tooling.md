# Reference: tooling

| Tool | File | Function |
|---|---|---|
| uv | `pyproject.toml`, `uv.lock` | Makes the environment and runs the commands. |
| ruff | `pyproject.toml` | Lints and formats the Python code. |
| ty | `pyproject.toml` | Examines the types. |
| pytest | `tests/` | Runs the tests. Set `MAI_IT=1` to run the tests against the container. |
| lefthook | `lefthook.yml` | Runs the git hooks. |
| Secrets check | `scripts/check_secrets.py` | Finds tokens and private keys before a commit. |
| Commit check | `scripts/check_commit.py` | Makes sure that a commit message obeys Conventional Commits. |
| Layer audit | `.agents/skills/mai-dev/scripts/audit.py` | Examines the imports between the layers and the public names of each module. |
| Go | `cli/go.mod` | Builds the host CLI. `go vet` and `go test` examine it. |
| Named session test | `scripts/it_named_sessions.sh` | Builds the CLI, and starts a test instance of the image with a scratch `MAI_HOME`. Then it does a test of the named sessions through the bridge and the hook, and removes the instance. In some calls, the chat gives only the label of its session as `session`, with no `login`. The key file is in the scratch folder, and the CLI uses no keychain item. At the end, the test makes sure that `mai uninstall` removed the keys of the instance from the key file. |
| CI | `.github/workflows/ci.yml` | Runs the checks, the tests, the image test, and the job "CLI (os)" on Linux, macOS, and Windows. |
| Release | `.github/workflows/release.yml` | Publishes a version on a tag `vX.Y.Z`. |
| GoReleaser | `.goreleaser.yaml` | Not used at this time. It can build the six binaries of the CLI, the checksums, the Homebrew formula, and the Scoop manifest. |
| Dependabot | `.github/dependabot.yml` | Opens update requests each week. |

## Hooks

| Hook | Jobs |
|---|---|
| pre-commit | `uv lock --check`, then ruff, ty, the secrets check and the layer audit |
| commit-msg | The commit check |
| pre-push | `pytest -q` |

Install the hooks one time in each clone:

```sh
lefthook install
```

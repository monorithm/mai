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
| CI | `.github/workflows/ci.yml` | Runs the checks, the tests and the image test. |
| Release | `.github/workflows/release.yml` | Publishes a version on a tag `vX.Y.Z`. |
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

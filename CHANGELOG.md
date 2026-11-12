# Changelog

All notable changes to this project are in this file. The format is
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.0.0] - 2026-11-12

The first release of mai (Mail for AI).

### Added

- One Linux container with PostgreSQL 18.6, Dovecot 2.4.5, Postfix 3.11.7, and the app.
  The app uses the bustan framework, with one module for each feature and a memory
  and a PostgreSQL adapter for each storage port.
- The public image `ghcr.io/monorithm/mai` for `linux/amd64` and `linux/arm64`. A pull
  does not ask for a login. A push to `main` updates `latest`, and a release tag `vX.Y.Z` pushes
  `X.Y.Z`, `X.Y`, and `X`.
- A run with no clone: `mai shim install DIR` in the image writes the host shims and a
  compose file to a host folder. `compose.yaml` has the project name `mai`.
- An MCP server with 22 tools on stdio (`mai mcp`) and on Streamable HTTP (`POST /mcp`).
- Agents are data: `mai agent add NAME [NAME ...]` and `mai agent list`. `mai init`
  makes only the system accounts `owner` and `board`.
- Session credentials: `mai session new`, `list`, `rotate`, and `revoke`, with
  `--credential-only`. `mai session new` for an unknown agent gives an error that
  tells you to use `mai agent add`.
- Thread claims with the error `THREAD_CLAIMED`, and proof of the sender of each mail.
  The router moves each new reply of a claimed thread to the session that holds the
  claim. It reads the agent list at start and each 30 seconds (`THREADS_AGENT_REFRESH`).
- The work board: items, sprints, assignment, status, and review. `mai project seed`
  takes `--supervisor` and `--worker`.
- `mai watch` and `mai hook` for notices of new mail. `mai hook --format` gives the
  hook output of each app: `plain`, `claude`, `cursor`, `copilot`, `codex`, or
  `antigravity`. `--event NAME` gives the hook event.
- `mai backup`, `restore`, and `import-maildir`. The import takes `--add-agents`,
  `--rename-header-prefix OLD=NEW`, and `--dry-run`.
- An owner UI for the session credentials and the board.
- Host shims for macOS, Linux, and Windows: `bin/mai` (sh), `bin/mai.ps1`
  (PowerShell), and `bin/mai.cmd` (Windows). `--save PATH` writes a credential to a
  host file with mode 0600. For `mcp`, the shim gives the text of
  `MAI_CREDENTIAL_FILE` to the container in `MAI_CREDENTIAL`.
- `.gitattributes` keeps LF for all text, and CRLF only for `*.cmd` and `*.ps1`.
- lefthook hooks, CI on macOS, Linux, and Windows, a release workflow, an image
  workflow, Dependabot, and `scripts/it_container.sh` for the container tests.
- Documentation: a tutorial, how-to pages, reference pages, and an explanation of the
  architecture. The agent skill `mai-dev` is in `.agents/skills/`.
- The license is the Apache License 2.0 (`LICENSE` and `NOTICE`).

### Removed

- The Maildir bridge of 0.5.0 and its connector tools `watch_notices` and
  `report_notified`. `mai import-maildir` imports the mail of that bridge.

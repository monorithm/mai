# Changelog

All notable changes to this project are in this file. The format is
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [3.0.1] - 2026-11-12

### Fixed

- `mai connect` writes the full path of the mai binary in the MCP entries and
  in the hooks. An app starts its MCP servers and hooks with its own `PATH`, thus
  the app did not find the command `mai` that 3.0.0 wrote. Run
  `mai connect APP --agent NAME` again: it replaces the entries of 3.0.0.
- `mai connect` and `mai disconnect` change only the entries of mai. Each other
  byte of the file of an app stays the same, and `mai disconnect` gives back the
  bytes from before `mai connect`.
- After the mai binary moves, `mai connect` replaces the entries of mai and adds
  no second entry.
- The diff of `mai connect` shows the removed lines before the added lines.
- In a hook group of Claude Code or Codex, `mai connect` and
  `mai disconnect --force` change only the hook of mai. The other hooks of the
  group stay.
- A second `mai connect` keeps each key that the user adds to a hook of mai or
  to its group, for example `"matcher"`. `mai disconnect --force` removes a
  group that holds only hooks of mai, also with a key of the user.
- A server of the user that runs a program named `mai` with arguments other than
  `mcp` is not an entry of mai, also in `config.toml`.
- `mai connect codex` finds the table of mai by its comment and its header.
  Thus it keeps the tables and the comments that Codex or the user adds after
  the table of mai. A second `mai connect codex` changes only the keys `command`
  and `args`, and keeps each key that the user adds to the table. A sub-table of
  the user, for example `[mcp_servers.mai.env]`, stays, and
  `mai disconnect codex --force` removes it with the table of mai.
- `mai disconnect codex` removes the lines of mai as whole lines, thus no two
  lines of the user join. The scan of `config.toml` reads multi-line strings,
  with escapes, and arrays on more than one line, as TOML does.
- A second `mai connect codex` keeps a comment of the user on the line of
  `command` or `args`.
- `mai disconnect --force` keeps a server of the user named `mai`, in
  `config.toml` and in a JSON file. In a hook set of 3.0.0 that holds hooks of
  the user, it removes only the hooks of mai.
- `mai disconnect --force` removes the entries in the correct sequence, thus it
  also removes a hooks file that `mai connect` made.
- `mai connect codex` gives an error, not a second table, for a table
  `mcp_servers.mai` in a different form of TOML, and for an inline table
  `mcp_servers = { ... }`.
- When the comment of mai above its table is gone, `mai disconnect codex` gives
  an error and keeps the record of the table.
- An MCP entry of mai keeps each key that the user adds, for example `env`. The
  hook of mai for Antigravity is one element of the array `PreInvocation`, thus
  a hook of the user in that array stays.
- `mai disconnect cursor` keeps `"version": 1` in a hooks file that holds hooks,
  also for a record of 3.0.0. It removes `version` after the other entries. When
  the file held hooks before `mai connect`, `version` goes again.
- Each empty object or array that mai made in a file goes at `mai disconnect`,
  also after an earlier disconnect kept one entry.
- After an upgrade from 3.0.0, `mai connect antigravity` moves the record of the
  hook set `mai` to the record of the hook. Thus `mai disconnect` keeps the
  hooks of the user.
- `mai disconnect --force` keeps an entry at the place of mai that is not an
  entry of mai.
- `mai uninstall` keeps a file of an app that `mai connect` made, when the file
  holds content of the user.
- `mai disconnect` gives no error for an entry of mai that the user removed.
- When an app writes its file before you agree to the diff of `mai connect`,
  the write of the app stays.
- `mai connect` writes through a symbolic link, also a link to a missing file,
  and the link stays. When the user replaces a file that mai made with a link,
  `mai disconnect` keeps the file that the link names.
- `mai disconnect` removes the empty folders that `mai connect` made for a file.
- `mai connect` and `mai disconnect` read a JSON file with a byte order mark,
  and keep the mark.
- The record of the table of mai in `config.toml` uses one kind of line break.
  Thus Codex, an editor, or git can change the line breaks, and the table counts
  as unchanged.
- `mai disconnect codex --force` also removes a table of mai with `args` on more
  than one line. It also removes the begin comment of mai above a comment of
  the user.
- A file with CRLF line breaks keeps CRLF. An empty object or array, and a file
  with only white space, come back byte for byte after `mai disconnect`.
- `mai connect` refuses a path of the binary that is not valid UTF-8, and a
  binary of `go run`.

## [3.0.0] - 2026-11-12

mai 3.0 has no compatibility with 2.x. There is no migration of data,
credentials, shims, or MCP entries. To remove a 2.x install, use
`mai uninstall --legacy`.

### Added

- The host CLI `mai`, one Go binary for macOS, Linux, and Windows, each amd64
  and arm64. It operates the container through the Engine API of Docker or
  Podman. You do not use `docker`, `podman`, or compose.
- Leases. An app gets a session only through a lease of the bridge
  `mai mcp --agent A`. The lease token is only in the memory of the bridge.
- A presence check for each new lease: Touch ID, Windows Hello, or the password
  of the user.
- The owner key and the agent keys in the system keychain. The server keeps only
  a hash of each key.
- A copy of each mail to the owner, in the system mailbox `copies`.
- An optional mail UI. `mai ui on`, `mai ui open`, and `mai ui off` control it.
  It is off by default.
- `mai uninstall` removes all the items that mai made. A manifest records each
  item.
- The CLI is a Go module. Install it with
  `go install github.com/monorithm/mai/cli/v3/cmd/mai@v3.0.0`. On macOS, the
  keychain and the presence check need cgo, thus the Xcode command line tools.

### Removed

- **Breaking:** the shims `bin/mai`, `bin/mai.ps1`, and `bin/mai.cmd`, the
  command `mai shim install`, and `compose.yaml`. The image has no share folder.
- **Breaking:** session credentials, credential files, `--credential-file`, and
  `MAI_CREDENTIAL_FILE`. A lease replaces them.
- **Breaking:** the MCP tool `login` and the argument `session`. The MCP
  contract is 3.0.0. The session of the lease is the session of each tool call.
- **Breaking:** the host ports 1993 (IMAPS) and 1587 (submission). The agents
  use only MCP.
- **Breaking:** the file `/data/secrets/owner.cred`. The CLI makes the owner key
  on the host and gives only its hash to the container.

### Changed

- **Breaking:** 3.0 uses the new volumes `mai3-data` and `mai3-spool`. It does
  not read 2.x data, and there is no import.
- The CI job "CLI (os)" replaces the job "Shims (os)". It builds the Go CLI and
  does a test of it on Linux, macOS, and Windows.

## [2.1.0] - 2026-11-12

### Added

- The MCP resource `mai://guide` tells an agent how to use mai. A client can read it
  with no login. Its tool table comes from the tool schemas. The contract version is
  2.1.0, because the server has the new capability `resources`.
- `MAI_DATA_DIR` in `compose.yaml` keeps `/data` in a folder on the host. With no
  value, the named volume `mai-data` holds it, as before.

### Changed

- The Postfix queue is on the volume `mai-spool` (`/var/lib/mai/spool`), not in
  `/data/postfix`. A host folder on macOS or Windows can hold no Unix socket. With no
  volume there, the container uses the tmpfs `/run/mai/spool` and logs a warning.
- `mai health` prints one line for each result. When it passes, the line names the
  daemons that run, for example `mai: healthy (postgres, dovecot, milter, postfix, app)`.
  When it does not pass, the line names the daemons that do not run, or the error of
  `/healthz`. The exit code does not change.

### Upgrade

- Write the compose file again, because the earlier file has no volume `mai-spool`:
  `docker run --rm -v "$HOME/.mai/bin:/out" ghcr.io/monorithm/mai:2.1.0 mai shim install /out --force`.
  Then pull and start the container again.
- The queue of an earlier container stays in `/data/postfix`, and the new queue
  starts empty. In normal use, the queue is empty.

## [2.0.1] - 2026-11-12

### Changed

- The build stage of the image uses `astral/uv` 0.13.7.
- To build the package, `hatchling` 1.33.0 or a newer version is necessary.
- `whoami` gives the version of the package also when the package metadata is
  not available.

### Added

- The README gives the steps for a pull that gives `denied`. The cause is a stored
  login for `ghcr.io` that the registry refuses.

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

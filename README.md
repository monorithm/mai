# mai (Mail for AI)

mai is a mail server for agents. Each agent gets a mailbox. The agents
send requests, replies and status to each other through MCP tools.

The container has PostgreSQL, Dovecot (IMAP), Postfix (SMTP) and the app. The
app is an MCP server and an optional mail UI. The mail domain is
`mai.internal`.

mai operates on macOS, Linux and Windows. All the servers are in one Linux
container. On the host, one binary, `mai`, operates the container through the
Engine API. You do not use `docker`, `podman`, or compose.

> [!NOTE]
> mai 3.0 is not compatible with 2.x. It uses new volumes and does not read
> 2.x data. Refer to [CHANGELOG.md](CHANGELOG.md).

## Requirements

| Host | Necessary software |
|---|---|
| macOS | Docker Desktop or Podman |
| Windows | Docker Desktop with the WSL 2 back end. Refer to [Run mai on Windows](docs/how-to/run-on-windows.md). |
| Linux | Docker Engine or Podman |

The CLI does not install an engine. If no engine is installed, `mai start`
stops and gives the install step for your OS.

## Quick start

1. Install the CLI with Go 1.27 or a newer version:

   ```sh
   go install github.com/monorithm/mai/cli/v3/cmd/mai@latest
   ```

   Go puts `mai` in the folder `bin` of `go env GOPATH`. Make sure that this
   folder is in your `PATH`. On macOS, install the Xcode command line tools
   first (`xcode-select --install`). The keychain and the presence check of
   macOS are possible only with cgo, and cgo must have a C compiler.

2. Start mai:

   ```sh
   mai start
   ```

   The CLI finds the engine and starts it if it is stopped. It pulls the image
   `ghcr.io/monorithm/mai` of its own version, makes the container, and waits
   until the container is healthy. At the first start, it makes the owner key
   and keeps it in the system keychain.

3. Add an agent:

   ```sh
   mai agent add cursor
   ```

   The CLI keeps the agent key in the system keychain. It does not show the
   key.

4. Connect an app to the agent:

   ```sh
   mai connect cursor --agent cursor
   ```

   The CLI shows the change to the MCP entry and the hooks of the app. It
   writes the change only after you agree. The MCP entry is
   `mai mcp --agent cursor`, and it holds no secret. The apps are `claude`,
   `cursor`, `copilot`, `codex`, and `antigravity`.

5. Start the app. When the app starts `mai mcp`, the OS shows a prompt for
   Touch ID, Windows Hello, or your password. Approve the prompt only if you
   started the app.

6. Optional: start the mail UI and open it:

   ```sh
   mai ui on
   mai ui open
   ```

   `mai ui open` does a presence check, then signs you in to the UI in your
   browser. `mai ui off` stops the UI. The UI is off by default.

7. To remove mai and all the items that it made:

   ```sh
   mai uninstall
   ```

   > [!CAUTION]
   > `mai uninstall` deletes all mail and all copies. To keep a copy, add
   > `--backup PATH`.

## Lifecycle commands

| Command | Action |
|---|---|
| `mai start` | Start the container. Make it if it does not exist. |
| `mai stop` | Stop the container. The volumes stay. |
| `mai status` | Show the health, the versions, the engine, the port, and the state of the UI. |
| `mai logs --follow` | Show the log of the container. |
| `mai doctor` | Examine the engine, the port, the disk space, the keychain, and the presence check. |
| `mai upgrade` | Pull the image of the new CLI version and make the container again. |

## The image

The image is `ghcr.io/monorithm/mai` in the GitHub Container Registry. The
image is public. For a pull, a login and a token are not necessary. The CLI
pulls the image with the same version as the CLI.

## Data

The volume `mai3-data` holds the data of mai: the database, the mail, and the
backups. The volume `mai3-spool` holds the Postfix queue. To keep the data in
a folder on the host, use `mai start --data-dir PATH`. The CLI writes the
choice to its configuration file.

## Addresses on the host

| Address | Service |
|---|---|
| `http://127.0.0.1:8765` | MCP (`/mcp`), API (`/api`), and the optional mail UI (`/ui`) |

The container publishes no IMAP port and no submission port. The agents use
only MCP.

## Documentation

Refer to [docs/README.md](docs/README.md).

## Development

A clone is necessary only to change mai. The server is Python with uv. The
host CLI is Go, in the folder `cli/`.

These commands make the image from the `Dockerfile`:

```sh
docker build --pull=false -t mai:dev .
```

These commands run the tests of the server and install the hooks:

```sh
uv sync
uv run pytest -q
lefthook install
```

These commands build and test the CLI:

```sh
cd cli
go build ./...
go vet ./...
go test ./...
```

Agents that change this project must use the skill `mai-dev`. Refer to
[the skill file](.agents/skills/mai-dev/SKILL.md).

## License

Apache License 2.0. Copyright 2026 Moses Gameli. Refer to [LICENSE](LICENSE)
and [NOTICE](NOTICE).

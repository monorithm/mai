# mai (Mail for AI)

mai is a mail server for agents. Each agent gets a mailbox. The agents
send requests, replies and status to each other through MCP tools.

The container has PostgreSQL, Dovecot (IMAP), Postfix (SMTP) and the app. The
app is an MCP server and a web UI. It is an IMAP and SMTP client for each
session. The mail domain is `mai.internal`.

mai operates on macOS, Linux and Windows. All the servers are in one Linux
container. On the host, a small shim runs the commands in the container.

## Requirements

| Host | Necessary software |
|---|---|
| macOS | Docker Desktop |
| Windows | Docker Desktop with the WSL 2 back end. Refer to [Run mai on Windows](docs/how-to/run-on-windows.md). |
| Linux | Docker Engine with the compose plugin |

A clone of the repository is not necessary to run mai. Git is necessary only
for development.

## The image

The image is `ghcr.io/monorithm/mai` in the GitHub Container Registry. The
image is public. For a pull, a login and a token are not necessary.

Each push to `main` updates the tag `latest`. Each release adds the tags
`X.Y.Z`, `X.Y` and `X`, for example `2.0.0`, `2.0` and `2`. To use a
different tag, set `MAI_TAG`. Refer to
[Configuration](docs/reference/configuration.md).

The image has the host shims and a compose file. The command
`mai shim install DIR` writes these files to a directory on the host:

- `mai`, the sh shim (mode 0755)
- `mai.ps1` and `mai.cmd`, the shims for Windows
- `compose.yaml`, the compose file. The command writes this file only if it
  is not there. To write it again, add `--force`.

## Quick start

The shims and the compose file go into the directory `.mai/bin` of your home
directory. The credential files go into the directory `.mai`.

### macOS and Linux

Use a terminal.

1. Make the directory for the shims:

   ```sh
   mkdir -p ~/.mai/bin
   ```

2. Write the shims and the compose file from the image:

   ```sh
   docker run --rm --user "$(id -u):$(id -g)" -v "$HOME/.mai/bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
   ```

3. Start the container and make sure that it is healthy:

   ```sh
   docker compose -f ~/.mai/bin/compose.yaml up -d
   ~/.mai/bin/mai health
   ```

4. Add the agent account, then make a session credential for the agent:

   ```sh
   ~/.mai/bin/mai agent add cursor
   ~/.mai/bin/mai session new cursor --label lane-a --save ~/.mai/cursor.lane-a.cred
   ```

5. Connect the agent to `~/.mai/bin/mai mcp`. In the MCP entry, use the full
   path: `/Users/<you>/.mai/bin/mai` on macOS, `/home/<you>/.mai/bin/mai` on
   Linux. Refer to [Connect an agent](docs/how-to/connect-an-agent.md).

### Windows

Use PowerShell. The examples use the shim `mai.cmd`.

1. Make the directory for the shims:

   ```powershell
   New-Item -ItemType Directory -Force "$env:USERPROFILE\.mai\bin" | Out-Null
   ```

2. Write the shims and the compose file from the image:

   ```powershell
   docker run --rm -v "$env:USERPROFILE\.mai\bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
   ```

3. Start the container and make sure that it is healthy:

   ```powershell
   docker compose -f "$env:USERPROFILE\.mai\bin\compose.yaml" up -d
   & "$env:USERPROFILE\.mai\bin\mai.cmd" health
   ```

4. Add the agent account, then make a session credential for the agent:

   ```powershell
   & "$env:USERPROFILE\.mai\bin\mai.cmd" agent add cursor
   & "$env:USERPROFILE\.mai\bin\mai.cmd" session new cursor --label lane-a --save "$env:USERPROFILE\.mai\cursor.lane-a.cred"
   ```

5. Connect the agent to `mai.cmd mcp`. In the MCP entry, use the full path
   `C:\Users\<you>\.mai\bin\mai.cmd`. Refer to
   [Connect an agent](docs/how-to/connect-an-agent.md).

### Update mai

1. Pull the new image:

   ```sh
   docker compose -f ~/.mai/bin/compose.yaml pull
   ```

2. Start the container again with the new image:

   ```sh
   docker compose -f ~/.mai/bin/compose.yaml up -d
   ```

3. Write the shims again. Use the `docker run` command of step 2 of the
   quick start.

On Windows, use the path `"$env:USERPROFILE\.mai\bin\compose.yaml"`. The
mail and the database stay in the volume of the container.

### A pull gives "denied"

If `docker pull ghcr.io/monorithm/mai` gives `denied: denied` or `403 Forbidden`, Docker
has a stored login for `ghcr.io` that the registry refuses. Docker sends a stored login
also for a public image. Remove the login, then pull again:

```bash
docker logout ghcr.io
```

If you use `ghcr.io` for private images too, log in again with a token that has the
scope `read:packages`.

### Keep the data in a folder on the host

By default, the named volume `mai-data` holds the data of mai: the database, the
mail, the secrets, and the backups. To keep the data in a folder on the host, set
`MAI_DATA_DIR` before you start the container. Use an absolute path, or a path that
starts with `./`:

```bash
MAI_DATA_DIR="$HOME/.mai/data" docker compose -f ~/.mai/bin/compose.yaml up -d
```

The Postfix queue stays on the volume `mai-spool`, because a host folder on macOS
or Windows can hold no Unix socket.

To move data from the volume to a folder, make a backup with `mai backup` first.
Then restore it in the new folder.

## Addresses on the host

| Address | Service |
|---|---|
| `http://127.0.0.1:8765` | MCP (`/mcp`), web UI (`/ui`), API (`/api`) and health (`/healthz`) |
| `127.0.0.1:1993` | IMAP with TLS |
| `127.0.0.1:1587` | SMTP submission |

## Documentation

Refer to [docs/README.md](docs/README.md).

## Development

A clone is necessary only to change mai. In the clone, these commands make
the image from the `Dockerfile` and start the container:

```sh
docker compose build
docker compose up -d
bin/mai health
```

These commands run the tests and install the hooks:

```sh
uv sync
uv run pytest -q
lefthook install
```

Agents that change this project must use the skill `mai-dev`. Refer to
[the skill file](.agents/skills/mai-dev/SKILL.md).

## License

Apache License 2.0. Copyright 2026 Moses Gameli. Refer to [LICENSE](LICENSE)
and [NOTICE](NOTICE).

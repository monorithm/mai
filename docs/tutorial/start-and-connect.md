# Tutorial: start mai and connect an agent

In this tutorial, you install the CLI `mai`, start the container, connect
Claude Code as the agent `claude`, and send a mail.

## What you learn

- How the CLI starts the container
- How an app gets a session through a lease
- How to send a mail to an agent

## Before you start

- Install Docker Desktop, Docker Engine, or Podman.
- Install Claude Code.
- A clone of the repository is not necessary.

## Step 1: Install the CLI

Install the CLI with Go 1.27 or a newer version. On macOS, install the Xcode command
line tools first (`xcode-select --install`). The CLI uses cgo to move the keys
of mai 3.0 and 3.1 from the keychain of macOS.

```sh
go install github.com/monorithm/mai/cli/v3/cmd/mai@latest
mai version
```

Make sure that the folder `bin` of `go env GOPATH` is in your `PATH`. On
Windows, refer to [Run mai on Windows](../how-to/run-on-windows.md).

## Step 2: Start mai

```sh
mai start
mai status
```

The CLI pulls the image `ghcr.io/monorithm/mai` of its own version, makes the
container `mai`, and waits until the container is healthy. At the first start,
the CLI makes the owner key and keeps it in its key file.

## Step 3: Add the agent

```sh
mai agent add claude
```

The CLI keeps the agent key in its key file. No session secret is on the
host.

## Step 4: Connect Claude Code

```sh
mai connect claude --agent claude
```

The CLI shows the MCP entry `mai mcp --agent claude` and the hooks. Type `y`
to agree. Then start Claude Code. The bridge gets a lease with the agent key,
and the server gives the session `claude.s1`.

## Step 5: Send a mail

In Claude Code, tell the agent to call the tool `whoami`. The result gives
the session `claude.s1`. Then tell the agent to send a mail to `claude`. The
next prompt of Claude Code shows the notice of the new mail.

## Why the design is like this

The server keeps all session credentials in the container. An app gets a
session only through a lease, and the agent key is necessary for a lease. The
agent key is in the key file of the CLI, and only your OS user can read that
file. Refer to [Architecture](../explanation/architecture.md).

## Remove mai

```sh
mai uninstall
```

> [!CAUTION]
> `mai uninstall` deletes all mail. To keep a copy, add `--backup PATH`.

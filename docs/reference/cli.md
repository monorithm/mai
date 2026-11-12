# Reference: CLI

The command `mai` runs in the container. On the host, use a shim. Each shim
runs `docker exec -i mai mai COMMAND ARGUMENTS`.

## Shims

The command `mai shim install DIR` writes the shims to DIR on the host.
The examples use the directory `~/.mai/bin`. In a clone, the shims are also
in the directory `bin`.

| Shim | Hosts | Shell |
|---|---|---|
| `mai` | macOS, Linux, WSL, Git Bash | POSIX sh |
| `mai.cmd` | Windows: cmd, PowerShell and apps that start a command | cmd |
| `mai.ps1` | Windows PowerShell 5.1 or PowerShell 7 | PowerShell |

`mai.cmd` runs docker directly for all commands, except these commands:

- `watch`
- `hook`
- `session new` with `--save`

For these commands, `mai.cmd` starts `mai.ps1` in the same directory.
Refer to [Run mai on Windows](../how-to/run-on-windows.md).

## Commands

| Command | Arguments | Function |
|---|---|---|
| `run` | | Starts the container controller: migrations, PostgreSQL, Dovecot, Postfix and the HTTP server. |
| `health` | | Shows the status of each child process. The exit code is 0 when all are healthy. |
| `init` | | Applies the migrations and makes the system accounts `owner` and `board`. |
| `agent add` | `NAME [NAME ...]` | Makes an agent account for each name. Gives the board account read access to the queue of the agent. |
| `agent list` | `[--json]` | Shows the agents, their status and the number of live sessions. |
| `session new` | `AGENT --label LABEL [--expires 7d] [--save PATH] [--credential-only] [--file PATH]` | Makes a session credential. The agent must exist. |
| `session list` | `[--agent AGENT] [--json]` | Shows the session logins. |
| `session rotate` | `LOGIN [--file PATH]` | Gives a session a new secret. |
| `session revoke` | `LOGIN` | Revokes a session. |
| `mcp` | | Starts the MCP server on stdio. |
| `serve-http` | `[--host 0.0.0.0] [--port 8080]` | Starts the MCP server and the UI on HTTP. |
| `watch` | `--credential-file PATH [--timeout 1500]` | Waits for new mail. The unit of the timeout is seconds. |
| `hook` | `--credential-file PATH [--format FORMAT] [--event NAME]` | Shows the notices of new mail one time. |
| `backup` | | Makes a backup of the database and the mail. |
| `restore` | `PATH` | Restores a backup. |
| `import-maildir` | `PATH [--add-agents] [--rename-header-prefix OLD=NEW] [--dry-run]` | Imports a maildir of version 0.5.0. |
| `shim install` | `DIR [--force]` | Writes the host shims and the compose file of the image to DIR. Refer to [Shim install](#shim-install). |
| `project seed` | `PROJECT --supervisor AGENT --worker AGENT [--lane NAME] [--goal TEXT] [--min-coverage 80]` | Makes or updates a board project with a supervisor and one worker lane. The default lane name is the worker name. |

## Shim install

`mai shim install DIR [--force]` runs in a temporary container. Use the flag
`-v` of `docker run` to attach a directory of the host to DIR. The command
writes these files to DIR:

| File | Content |
|---|---|
| `mai` | The sh shim, with mode 0755. |
| `mai.ps1` | The PowerShell shim. |
| `mai.cmd` | The cmd shim. |
| `compose.yaml` | The compose file of the repository, without `build: .`. |

The command always writes the shims again. It writes `compose.yaml` only if
the file is not there. With `--force`, it also writes `compose.yaml` again.
Thus your changes to `compose.yaml` stay after an update of the shims.

The compose file has these values:

- The project name `mai` and the container name `mai`.
- The image `ghcr.io/monorithm/mai:${MAI_TAG:-latest}`.
- The ports `127.0.0.1:8765` (HTTP: `/mcp`, `/ui`, `/healthz`),
  `127.0.0.1:1993` (IMAPS) and `127.0.0.1:1587` (submission).

On macOS and Linux, use `--user` to give the files your user and group:

```sh
docker run --rm --user "$(id -u):$(id -g)" -v "$HOME/.mai/bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
```

On Windows (PowerShell):

```powershell
docker run --rm -v "$env:USERPROFILE\.mai\bin:/out" ghcr.io/monorithm/mai:latest mai shim install /out
```

## Flags of session new

| Flag | Where | Function |
|---|---|---|
| `--save PATH` | Shim on the host | Writes only the credential line to PATH on the host. On macOS and Linux, the file gets mode 0600. On Windows, the file gets the access rules of the user profile. The shim makes the directory if it is not there. |
| `--credential-only` | Container | Shows only the credential line. The shim uses this flag for `--save`. |
| `--file PATH` | Container | Writes the credential to PATH in the container. The file gets mode 0600. |
| `--expires` | Container | A number and a unit: `h` (hours) or `d` (days). The default is `7d`. |

Without `--save`, `--credential-only` and `--file`, the command shows the
login, the credential and the expiry date.

## Agent names

An agent name has 1 to 48 characters: lower-case letters, digits and `-`. The
first character is a letter or a digit. A name cannot have a dot, because the
dot separates the agent and the label in a session login. The names `owner`
and `board` are system accounts. `agent add` for an agent that exists makes
no change.

The router reads the agent list when it starts and then each 30 seconds. It
starts a queue worker for each new agent. The variable
`THREADS_AGENT_REFRESH` sets this time in seconds.

## Flags of hook

| Flag | Function |
|---|---|
| `--credential-file PATH` | The credential file. With `-`, the first line of stdin is the credential, and the remaining lines are the hook data of the app (JSON or empty). |
| `--format FORMAT` | The output format of the app that runs the hook: `plain`, `claude`, `cursor`, `copilot`, `codex` or `antigravity`. The default is `plain`. |
| `--event NAME` | The name of the hook event of the app, for example `Stop`. |

## Flags of import-maildir

The import puts the mail of each folder of the maildir into the queue of the
account with the same name as the folder.

| Flag | Function |
|---|---|
| `--add-agents` | Makes the agent account of each folder that has no account. Without this flag, each mail of a folder with no account is an error (`UNKNOWN_ACCOUNT`). |
| `--rename-header-prefix OLD=NEW` | Changes each header name that starts with OLD to start with NEW. The match does not use the case of the letters. The body does not change. You can use the flag more than one time. |
| `--dry-run` | Counts the mails and writes nothing. |

## Exit codes of watch

| Code | Meaning |
|---|---|
| 0 | New mail is in the inbox. |
| 2 | The login was not correct, or the shim cannot read the credential file. |
| 3 | The timeout occurred. |
| 4 | The server version changed. |

## Credential files on the host

For `watch` and `hook`, the shim reads the credential file on the host. Then
the shim sends the credential to the container on stdin
(`--credential-file -`). Thus a mount for the credential files in the
container is not necessary. A path that starts with `~/` gets the home directory.

For other commands, a credential path is a path in the container.

A hook never stops the app. On an error, the shim shows nothing and gives the exit
code 0.

## Shim settings

| Variable | Function | Default |
|---|---|---|
| `MAI_CONTAINER` | The name of the container. | `mai` |
| `MAI_DOCKER` | The docker binary. | `docker` |
| `MAI_CREDENTIAL_FILE` | The credential file of `watch` and `hook` when the command has no `--credential-file`. | Empty |

Set a variable in the shell before you start the shim.

- macOS and Linux:

  ```sh
  MAI_CONTAINER=mai-test ~/.mai/bin/mai health
  ```

- Windows (PowerShell):

  ```powershell
  $env:MAI_CONTAINER = "mai-test"
  & "$env:USERPROFILE\.mai\bin\mai.cmd" health
  ```

# Reference: CLI

mai has two command lines with the name `mai`:

- The host CLI, a Go binary on the host. You use only this command.
- The server CLI in the container. The host CLI runs it. For recovery,
  `mai exec -- ARGS` runs it after a presence check.

## Host CLI

| Command | Function |
|---|---|
| `mai start [--data-dir PATH]` | Finds the engine and starts it if it is stopped. Pulls the image of the CLI version, makes the container, starts it, and waits until it is healthy. |
| `mai stop` | Stops the container. The volumes stay. |
| `mai pause`, `mai resume` | Freezes and resumes the container. |
| `mai status` | Shows the health, the versions of the CLI and of the image, the engine, the data location, the port, and the state of the UI. |
| `mai logs [--follow]` | Shows the log of the container. |
| `mai doctor` | Examines the engine, the port, the free disk space, the keychain, and the presence check. |
| `mai upgrade` | Pulls the image of the new CLI version and makes the container again. |
| `mai uninstall [--yes] [--backup PATH] [--legacy]` | Removes all items of the manifest. `--legacy` removes a 2.x install. |
| `mai connect APP --agent A` | Writes the MCP entry and the hooks for the app, after you agree. |
| `mai disconnect APP` | Removes the entries that `mai connect` wrote. |
| `mai agent add NAME`, `mai agent list`, `mai agent remove NAME` | Manages the agent accounts through the owner API. |
| `mai session list` | Shows the sessions, their leases, and the app and folder of each lease. |
| `mai session end LOGIN` | Ends the lease of a session. |
| `mai mcp --agent A` | The bridge. An app starts it. It gets a lease and bridges stdio to `/mcp`. |
| `mai hook --format F --event E` | Shows the hook output of the app. It reads the notice file of its bridge. |
| `mai ui on`, `mai ui off` | Starts or stops the mail UI. |
| `mai ui open` | Does a presence check, then opens the UI in the browser and signs you in. |
| `mai backup`, `mai restore STAMP` | Makes or restores a backup through the owner API. |
| `mai exec -- ARGS` | Runs the server CLI in the container, after a presence check. |
| `mai version` | Shows the version of the CLI. |

`APP` is one of `claude`, `cursor`, `copilot`, `codex`, and `antigravity`.
No command shows a session secret.

## Server CLI

### Commands

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
| `project seed` | `PROJECT --supervisor AGENT --worker AGENT [--lane NAME] [--goal TEXT] [--min-coverage 80]` | Makes or updates a board project with a supervisor and one worker lane. The default lane name is the worker name. |

## Flags of session new

| Flag | Where | Function |
|---|---|---|
| `--credential-only` | Container | Shows only the credential line. |
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
| 2 | The login was not correct, or the command cannot read the credential file. |
| 3 | The timeout occurred. |
| 4 | The server version changed. |

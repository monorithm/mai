# Reference: CLI

mai has two command lines with the name `mai`:

- The host CLI, a Go binary on the host. You use only this command.
- The server CLI in the container. The host CLI runs it. For recovery,
  `mai exec -- ARGS` runs it.

## Host CLI

| Command | Function |
|---|---|
| `mai start [--data-dir PATH]` | Finds the engine and starts it if it is stopped. Pulls the image of the CLI version, makes the container, starts it, and waits until it is healthy. |
| `mai stop` | Stops the container. The volumes stay. |
| `mai pause`, `mai resume` | Freezes and resumes the container. |
| `mai status` | Shows the health, the versions of the CLI and of the image, the engine, the data location, the port, and the state of the UI. |
| `mai logs [--follow]` | Shows the log of the container. |
| `mai doctor` | Examines the engine, the port, the free disk space, the key file, and the keychain items of mai 3.0 and 3.1. It shows no key. On macOS, it reads no key and shows no prompt. |
| `mai upgrade` | Moves the keys of the instance from the keychain of mai 3.0 and 3.1 to the key file. Then pulls the image of the new CLI version and makes the container again. |
| `mai uninstall [--yes] [--backup PATH] [--legacy]` | Removes all items of the manifest, the keys of the instance in the key file, and each keychain item of the instance. `--legacy` removes a 2.x install. |
| `mai connect APP --agent A` | Writes the MCP entry and the hooks for the app, after you agree. |
| `mai disconnect APP` | Removes the entries that `mai connect` wrote. |
| `mai agent add NAME`, `mai agent list`, `mai agent rotate NAME`, `mai agent remove NAME` | Manages the agent accounts through the owner API. The CLI keeps each agent key in the key file. `rotate` gives the agent a new key. |
| `mai session new AGENT --label L` | Makes the named session `AGENT.L`. Shows only the login. |
| `mai session new AGENT --count N` | Makes each session of `AGENT.s1` to `AGENT.sN` that does not exist. Shows only the login of each new session. |
| `mai session list [--agent AGENT] [--json]` | Shows the sessions, the label and the state of each session, the lease, and the app and folder of each lease. |
| `mai session rotate LOGIN` | Gives the session a new internal secret and ends its lease. |
| `mai session revoke LOGIN [--yes]` | Closes the session and ends its lease. This change is permanent. Type `revoke` to agree, or use `--yes`. |
| `mai session end LOGIN` | Ends the lease of a session. |
| `mai mcp --agent A` | The bridge. An app starts it. It gets a lease and bridges stdio to `/mcp`. The bridge, not the server, does the tools `login` and `logout`, and reads the argument `session` of each tool call. Refer to [Bridge](#bridge). |
| `mai hook --format F --event E` | Shows the hook output of the app. It reads the notice file of its bridge and shows one line for each session with new mail. |
| `mai ui on`, `mai ui off` | Starts or stops the mail UI. |
| `mai ui open` | Opens the UI in the browser and signs you in. |
| `mai backup`, `mai restore STAMP` | Makes or restores a backup through the owner API. |
| `mai exec -- ARGS` | Runs the server CLI in the container. |
| `mai version` | Shows the version of the CLI. |

`APP` is one of `claude`, `cursor`, `copilot`, `codex`, and `antigravity`.
No command shows a session secret.

A label has 1 to 32 characters: lower-case letters, digits, and `-`. The
first character is a letter or a digit. The commands `rotate`, `revoke`, and
`end` also accept the address of a session, for example
`cursor.lane-a@mai.internal`.

A label `s` with only digits after it, for example `s1`, `s9`, or `s01`, is a
label of the pool. Only the pool and `mai session new AGENT --count N` make
sessions with these labels. `mai session new AGENT --label s1` gives
`BAD_LABEL` and exit code 2, and it does not call the server. To make pool
sessions, use `--count N`.

## Bridge

`mai mcp --agent A` gets the pool lease of the agent when it starts. Then it
gets one lease for each named session that a chat uses. The bridge reads the
agent key from the key file at each lease request. Thus a new key from
`mai agent rotate` applies at the next lease request. When the key file has
no key for the agent, the result is `BAD_KEY`.

### Named sessions

A chat gives the label of its named session as the argument `session` in each
tool call, for example `session: "lane-1"`. The login of the session, for
example `cursor.lane-1`, is also correct. No `login` call is necessary:

1. When the bridge has no lease for that session, the first call does the
   steps of `login`. The bridge sends one lease request for the session,
   keeps the lease with a handle and a heartbeat, and then sends the call.
2. Each subsequent call with the same label, login, or handle uses the same
   lease. Calls at the same time for the same session wait for the first
   lease request, thus they make one lease request. Each of these calls gets
   the result of that request: the lease or the tool error.
3. When the lease request is not successful, the call gets the tool error of
   `login` with the path `session`. Examples are `BAD_LABEL` for a label of
   the pool, `SESSION_BUSY`, and `SERVER_UNAVAILABLE`. When the key file has
   no key for the agent, the code is `BAD_KEY`. When the bridge cannot read
   the key file, the code is `KEY_FILE_ERROR`. A refusal keeps no lease, thus
   the next call sends a new lease request.
4. A `login` to a session that the bridge holds gives the handle of that
   lease, also when a call with the label got the lease.
5. A `logout` with the label, the login, or the handle ends the lease of the
   named session. The next call with the label gets a new lease.

The bridge removes the argument `session` before it sends the call. A value
with the form of a handle is always a handle. A value that is not a label, a
login, or a handle gives `INVALID_ARGUMENT`. The notice file and `mai hook`
show each named session of the bridge, also a session that a call with a
label got.

### Pool lease with no session

When no pool session of the agent is free, the server gives a pool lease
with no session (`"session": null`). The bridge starts, and its log tells
that no pool session is free. With this lease:

- `initialize`, `ping`, `tools/list`, the resources, and the prompts
  operate.
- A call with the label of a named session operates, and `login` gives a
  handle for a named session. Each call with that handle operates.
- A tool call with no argument `session` gets the tool error
  `NO_FREE_SESSION` with `retry_safe` true. When a pool session is free, the
  server binds it to the lease, and the call operates as that session.
- The notice file has no pool entry. After each tool call on the pool lease,
  the bridge sends a heartbeat immediately. When a heartbeat gives the pool
  session, the bridge writes the notice file again with the pool entry.

Each time that the bridge gets a new lease, it writes the notice file
immediately. A heartbeat or a tool call can get the new lease. Thus the
notice file always has the pool session of the current pool lease, or no
pool entry when the pool lease has no session.

### Errors of a lease request

When the server does not know a lease, for example after a restart of the
container, the bridge sends a lease request again. This table gives the
result of an error of that lease request.

| Error | Named session | Pool lease |
|---|---|---|
| HTTP 4xx with an error code, for example `SESSION_REVOKED`, `SESSION_BUSY`, or `BAD_LABEL` | The handle ends. The call gets the code. A subsequent call with the handle gets `SESSION_UNKNOWN`. A subsequent call with the label sends a new lease request. | The bridge continues. The call gets the code, for example `NO_FREE_SESSION`. |
| `BAD_KEY`: the agent key is not valid | The handle ends. | The bridge stops with exit code 1. |
| HTTP 5xx, an error with no code, or a network error | The handle stays. | The bridge continues. |
| The bridge cannot read the agent key from the key file or from its keychain item | The handle stays. | The bridge continues. |

For an error of the server, these rules apply. When the bridge continues or
the handle stays, the call gets a tool error with the code of the server.
When the server gives no code, the code is `SERVER_UNAVAILABLE`. The tool error has the path
`session` and `retry_safe` true. The next call or heartbeat sends the lease
request again.

When the bridge cannot read the agent key, the error is of the host, not of
the server. The call gets the code `KEY_FILE_ERROR` with `retry_safe` false.
The message gives the step, for example `chmod 600 PATH` for a key file that
other users can read. When a different process holds the lock of the key file
for more than 10 seconds, the code is `BUSY` with `retry_safe` true. A call
with the argument `session` and a `login` get the path `session`. A call with
no `session` gets no path.

A login that gets an HTTP 5xx error, an error with no code, or a network
error also gives a tool error with `retry_safe` true. This is also true when
the server gives `retry_safe` false, for example with HTTP 502
`LOGIN_FAILED`. A login that gets an HTTP 4xx error gives the `retry_safe`
of the server.

### Logout and stop

A logout ends the heartbeat loop and the lease of the session before it
removes the handle. A login or a first call with the label of the same session
in the same bridge waits for the logout. Thus it does not get `SESSION_BUSY`
from the lease that the logout ends. When a logout ends the lease during a
call with the label, the server does not do the call. The bridge then gets a
new lease for the session and sends the call again.

The bridge stops when the app closes the stdin of the bridge. On Unix, the
bridge also stops on SIGINT, SIGTERM, or SIGHUP. On Windows, it stops on
Ctrl+C or when the console closes. Before it stops, it ends each lease and
removes its notice file. The bridge ends the leases at the same time, and it
waits a maximum of 10 seconds for the end of each lease.

The server makes a lease before it sends the result of the lease request.
Thus the bridge does not stop a lease request that it sent. When a logout
or a stop occurs during a lease request, the bridge waits for the result,
for a maximum of 30 seconds. Then it ends that lease also. Thus no lease of
the bridge stays on the server after a logout or a stop.

## Key file

The host CLI keeps the owner key and the agent keys of each instance in one
key file:

| OS | Key file |
|---|---|
| macOS | `~/Library/Application Support/mai/state/keys.json` |
| Linux | `$XDG_STATE_HOME/mai/keys.json`, by default `~/.local/state/mai/keys.json` |
| Windows | `%LocalAppData%\mai\state\keys.json` |

- The file is JSON. Its accounts are `owner@INSTANCE` and
  `agent:NAME@INSTANCE`. No command shows a key.
- The file has the mode 0600, in a folder with the mode 0700. On Unix, the CLI
  refuses a key file that a group or other users can read. The error gives
  the command that corrects the mode, for example `chmod 600 PATH`.
- Each write is atomic: a temporary file in the same folder, an fsync, and a
  rename. A lock on the file `keys.json.lock` lets one process at a time read
  or change the key file. Thus two CLIs or bridges at the same time do not
  lose a key.
- There is no presence check. A program that can use the Docker socket
  controls a self-run instance. Thus a keychain and a prompt give no
  protection from such a program.

### Keys of mai 3.0 and 3.1

mai 3.0 and 3.1 kept the keys in the system keychain, with the service
`dev.mai` and the same account names.

- When the key file has no key for an account, the CLI reads the keychain item
  of the account. It writes the key to the key file, and then it deletes the
  keychain item. On macOS, the read can show one prompt for each item.
- When no keychain is available and the manifest records a keychain item for
  the account, the command gives an error with a step. Thus `mai start` does
  not make a new owner key in place of the key of the keychain item. The
  versions 3.0 and 3.1 did not operate with no keychain. Thus with no record,
  no item exists.
- `mai upgrade` moves all the keys of the instance. When the key file has the
  same key for the account of a keychain item, `mai upgrade` deletes the
  keychain item. A keychain item stays when the key file has a different key
  for its account. Then `mai upgrade` gives a step and the exit code 1. For
  the owner key, `mai upgrade` does a test of the two keys on the server. When
  the server accepts only the key of the key file, `mai upgrade` deletes the
  keychain item. Thus no command deletes or replaces a key that the server
  accepts.
- `mai agent rotate NAME` deletes the keychain item of the agent, because the
  server then accepts only the new key.
- `mai doctor` tells about each keychain item of the instance. When no
  keychain is available and the manifest records keychain items of the
  instance, `mai doctor` gives a warning with a step.
- `mai uninstall` removes the keys of the instance from the key file and each
  keychain item of the instance. When the key file holds the keys of a
  different instance, the key file stays. When a keychain item of the
  instance stays, for example because no keychain is available,
  `mai uninstall` gives a step and the exit code 1. Then the manifest stays
  with the record of each such item, and the binary also stays. Thus a
  subsequent `mai uninstall` can remove the items.
- With `MAI_HOME`, the CLI keeps all its files in that folder, and it uses no
  keychain item.

## Server CLI

### Commands

| Command | Arguments | Function |
|---|---|---|
| `run` | | Starts the container controller: migrations, PostgreSQL, Dovecot, Postfix and the HTTP server. |
| `health` | | Shows the status of each child process. The exit code is 0 when all are healthy. |
| `init` | | Applies the migrations and makes the system accounts `owner` and `board`. |
| `agent add` | `NAME [NAME ...]` | Makes an agent account for each name. Gives the board account read access to the queue of the agent. |
| `agent list` | `[--json]` | Shows the agents, their status and the number of live sessions. |
| `session new` | `AGENT --label LABEL`, or `AGENT --count N` | Makes the named session `AGENT.LABEL`, or each pool session `AGENT.s1` to `AGENT.sN` that does not exist. Shows only the login of each session. The agent must exist. |
| `session list` | `[--agent AGENT] [--json]` | Shows the sessions, their status, and the time of the last login. |
| `session rotate` | `LOGIN` | Gives a session a new internal secret. The lease of the session ends at its next call. |
| `session revoke` | `LOGIN` | Closes a session for good. The lease of the session ends at its next call. |
| `mcp` | | Starts the MCP server on stdio. |
| `serve-http` | `[--host 0.0.0.0] [--port 8080]` | Starts the MCP server and the UI on HTTP. |
| `watch` | `--credential-file PATH [--timeout 1500]` | Waits for new mail. The unit of the timeout is seconds. |
| `hook` | `--credential-file PATH [--format FORMAT] [--event NAME]` | Shows the notices of new mail one time. |
| `backup` | | Makes a backup of the database and the mail. |
| `restore` | `PATH` | Restores a backup. |
| `import-maildir` | `PATH [--add-agents] [--rename-header-prefix OLD=NEW] [--dry-run]` | Imports a maildir of version 0.5.0. |
| `project seed` | `PROJECT --supervisor AGENT --worker AGENT [--lane NAME] [--goal TEXT] [--min-coverage 80]` | Makes or updates a board project with a supervisor and one worker lane. The default lane name is the worker name. |

## Session commands

No session command shows a session secret or writes a credential file. The
server keeps each secret in the container.

A label has 1 to 32 characters: lower-case letters, digits and `-`. The first
character is a letter or a digit. A named session has no expiry. Only a revoke
closes it.

A label `s` with only digits after it, for example `s1`, is a label of the
pool. `session new AGENT --label s1` gives `BAD_LABEL`. Use
`session new AGENT --count N` to make the pool sessions.

A lease request with no session name gets a pool session. The pool gives only
the sessions with a pool label, and never a named session. A revoked pool
session does not hold a place. The pool has a maximum of 8 open pool sessions,
thus a new pool session can be `s9` or more.

When no pool session is free, the lease has no session. A lease request for a pool
label, for example `s2` or `cursor.s2`, gives `BAD_LABEL`. Thus a login never
gets a pool session.

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

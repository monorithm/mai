# Reference: CLI

mai has two command lines with the name `mai`:

- The host CLI, a Go binary on the host. You use only this command.
- The server CLI in the container. The host CLI runs it. For recovery,
  `mai exec -- ARGS` runs it.

## Host CLI

| Command | Function |
|---|---|
| `mai start [--data-dir PATH]` | Finds the engine and starts it if it is stopped. Pulls the image of the CLI version, makes the container, starts it, and waits until it is healthy. When the server has an owner key, `mai start` does not show the text of the server about `--replace`. |
| `mai stop` | Stops the container. The volumes stay. |
| `mai pause`, `mai resume` | Freezes and resumes the container. |
| `mai status` | Shows the health, the versions of the CLI and of the image, the engine, the data location, the port, and the state of the UI. |
| `mai logs [--follow]` | Shows the log of the container. |
| `mai doctor` | Examines the engine, the port, the free disk space, the key file, and the keychain items of mai 3.0 and 3.1. It shows no key. On macOS, it reads no key and shows no prompt. |
| `mai upgrade` | Moves the keys of the instance from the keychain of mai 3.0 and 3.1 to the key file. Then pulls the image of the new CLI version and makes the container again. |
| `mai uninstall [--yes] [--backup PATH] [--legacy]` | Removes the items of the instance in the manifest, its keys in the key file, and each keychain item of the instance. The items of a different instance stay. `--legacy` removes a 2.x install. |
| `mai guide [--app APP] [--agent NAME] [--json]` | Shows an AI agent the state of mai and the steps to connect its app. It changes nothing. Refer to [mai guide](#mai-guide). |
| `mai connect APP --agent A [--dry-run \| --yes]` | Writes the MCP entry and the hooks for the app, after you agree. Refer to [mai connect](#mai-connect). |
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

### mai guide

`mai guide` is the resource for an AI agent. The agent runs it to find mai
and to connect its app. The user can tell the agent: "Run mai guide, and
connect this app to mai."

The command changes nothing. It writes no file, starts no engine or
container, pulls no image, and shows no key. It reads only these items:

- `config.json`, the key file, and the manifest. It reads no keychain item.
- The engine, when it runs: the container of the instance and the image.
  All the calls to the engine have a time limit of 6 seconds.
- The server, only while the container of the instance runs:
  `GET /api/version`, and `GET /api/agents` with the owner key. Each call
  has a time limit of 3 seconds. The CLI sends these requests only to the
  host port of the container.
- The files of the app, as `mai connect --dry-run` reads them.

| Flag | Function |
|---|---|
| `--app APP` | The app of the agent: `claude`, `cursor`, `copilot`, `codex`, or `antigravity`. With no flag, the CLI examines the chain of parent processes. The first parent process of a known app gives the app. |
| `--agent NAME` | The name of the agent account. The default is the name of the app. |
| `--json` | Shows the same content as one JSON object. |

An unknown app, a bad agent name, or a bad flag gives the exit code 2.
When the CLI cannot read a part of the state, the exit code is also 0.
Then the field of that part is null, and the field `reason` of its object
tells the cause.

The text has these parts:

1. A short text about mai, for an AI agent.
2. The state: the CLI version, the instance, the engine, the container, and
   the server. Also the owner key, the app, the agent, and the connection.
3. The steps that are not done, in sequence. Each step gives its command and
   its change. The agent gets the approval of the user in the chat before
   each step that changes something.
4. How to use a named session (a lane).
5. The rules for an agent.

The steps:

| `id` | Step | `by` |
|---|---|---|
| `confirm-app` | The CLI found the app in the chain of parent processes, and that app can be incorrect. For example, the chain does not show an agent CLI that runs on Node.js in the terminal of a different app. The agent tells the app to the user, and gets a confirmation before a step for the app. This step is the first step. | `agent` |
| `install-engine` | No engine is installed. The user installs Docker Desktop, Docker Engine, or Podman. | `user` |
| `start` | `mai start`. It can start the engine. At the first start, it can download the image (approximately 550 MB). If its output gives a step with `--replace`, the agent does not do that step. It runs the guide again. | `agent` |
| `select-app` | The CLI cannot find the app. The agent runs `mai guide --app APP`. The guide gives no subsequent step. | `agent` |
| `rotate-agent-key` | The agent account exists on the server, but the key file has no key for it. The agent runs `mai agent rotate NAME` only when the user tells it to do this. | `agent` |
| `add-agent` | `mai agent add NAME`. | `agent` |
| `show-change` | `mai connect APP --agent NAME --dry-run`. The agent shows the change to the user. | `agent` |
| `connect` | `mai connect APP --agent NAME --yes`, after the user agrees. | `agent` |
| `restart-app` | The user starts the app again, or starts its MCP server `mai` again. | `user` |
| `check` | The agent calls the tool `whoami` of the MCP server `mai`, and reads the resource `mai://guide`. This step is always the last step. When the chain of parent processes gives a different app, the user does this check in the app of the guide. | `agent` or `user` |

When mai runs, the agent account exists, and the app has the entries of mai
for the agent, only the step `check` stays.

After each step, the agent runs the guide again with the same flags. The
text and the field `rerun` give this command. A command with no flags can
give the steps of a different app or agent.

`mai start`, `mai agent add`, and `mai connect` cannot correct some states.
For these states, the guide gives only one step of the user. The agent tells
the cause to the user, and stops:

| `id` | State |
|---|---|
| `config-error` | The CLI cannot read `config.json`. The user corrects the file. |
| `legacy-container` | A container that mai 3 did not make has the name of the instance. For the container of a 2.x install, the step gives the command `mai uninstall --legacy`. The agent runs it only when the user tells it to do it. The other choice is a different instance, with `mai start --instance NAME`. |
| `container-error` | The engine runs, but the CLI cannot read the container of the instance. |
| `key-file-error` | The CLI cannot read the key file, for example because of its mode. The user corrects the key file. |
| `owner-key` | The server runs, but the key file has no owner key, or the server refuses it. The step gives the command `mai exec -- mai owner set-key --replace`, which replaces the owner key of the server. The agent runs it only when the user tells it to do it. Refer to [Key file](#key-file). |

The part State quotes each error of a different part of the CLI as code,
between backticks. Such an error can give a command. That command is not a
step of the guide.

The fields of `mai guide --json`:

| Field | Value |
|---|---|
| `for` | Always `"ai-agent"`. |
| `about` | The short text about mai. |
| `cli_version` | The version of the CLI. |
| `instance` | The instance of `config.json`. Null when the CLI cannot read `config.json`. |
| `reason` | The cause when `instance` is null. Else null. |
| `engine` | `running` and `installed` (true or false), and `name`: the name, the version, and the address of the engine. |
| `container` | `name`, `exists`, `running` (the container runs and is not paused), `state`, `image`, `image_local`, and `port` (the host port). |
| `server` | `url`, `running`, `version`, and `contract`. |
| `owner_key` | `exists`: the key file has the owner key. `accepted`: the owner API accepts the owner key of the key file. `accepted` is null when the CLI did not send the key, for example because the server does not run. |
| `app` | `name`, `title`, `found_by` (`"flag"`, `"process"`, or null), `connected`, and `apps` (the names of the apps). `connected` is true when `mai connect` for the app and the agent has no change. |
| `agent` | `name`, `exists`, `key` (the key file has the key of the agent), and `on_server` (the owner API lists the agent). `exists` is true when the key file has the key and the owner API lists the agent. `exists` is null when the CLI did not get the list of the agents from the server. Then `reason` tells the cause. |
| `rerun` | The command that gives the guide again, with the app and the agent of this run, for example `mai guide --app cursor --agent cursor-b`. The agent runs it after each step. Add `--json` for the JSON form. |
| `steps` | The steps that are not done, in sequence. Each step has `id`, `text`, `by` (`"agent"` or `"user"`), `command` (a command, or null), `tool` and `resource` (for the step `check`, else null), `changes`, `ask_user`, and `done`. `ask_user` is true for each step that changes something, and for `show-change`, because the diff can show lines of the files of the user. It is also true for `confirm-app`, because the user confirms the app. `done` is always false. |
| `lanes` | `text`, `command`, `example`, `argument`, `login_call` (always false), and `ask_user`. |
| `rules` | The rules for an agent, as text. |

Each object other than `steps`, `lanes`, and `rules` also has the field
`reason`. It tells the cause when a field of the object is null, and it can
give more data about a value. Else it is null.

### mai connect

`mai connect APP --agent NAME` shows the path of the mai binary and the diff
of the files of the app. Then it shows the prompt `Write these changes?
[y/N]`, and writes the change only after you type `y`.

- `--yes` writes the change with no prompt.
- `--dry-run` shows the same text, and then writes nothing: no file of the
  app and no record in the manifest. The exit code is 0.
- `--dry-run` and `--yes` together give the exit code 2 and the usage line.
- With no flag, when the input is not a terminal, the CLI writes one line to
  stderr for an AI agent. The line tells the agent to show the change with
  `--dry-run`, and to use `--yes` only after the user agrees. Then the CLI
  reads the response to the prompt from the input. An empty input gives no
  change and the exit code 1.

### Instances

The lifecycle commands take `--instance NAME`. The default is the instance
of `config.json`. The file `config.json` names one instance, with its port,
its image, and its data folder. When `--instance` names a different instance,
the CLI does not use these values of `config.json`:

- For an instance that has a container, the CLI reads the port and the data
  folder from that container. The port is the host port of `8080/tcp`, and
  the data folder is the mount at `/data`. This rule also applies to the
  instance of `config.json`. A start and an upgrade do not change the port
  or the data folder of a container. Thus `mai start` with a different
  `--port` or `--data-dir` tells that the flag has no effect. After the
  start, `config.json` gets the port and the data folder of the container.
- For a new instance, `mai start` uses `--port`, `--data-dir`, and
  `--image`, or the defaults. It refuses a port that a different program
  uses. It also refuses a data folder that a container of a different
  instance mounts.
- After a successful start, `config.json` names the instance of the start,
  with its port and its data folder. A start that is not successful does not
  change `config.json`.
- `mai status` and `mai doctor` show and examine the port of the container
  of the instance. `mai status` sends the owner key only to that port.
  `mai doctor` reports a problem when the port of `config.json` is not the
  host port of the container of its instance. The step is `mai start`,
  which writes the port of the container to `config.json`. `mai status`
  shows the line `server` only while the container runs, because a
  different program can use the port of a stopped container.
- `mai upgrade` does the check of the owner keys only on the port of the
  server of the instance. That port is the host port of the container of
  the instance, while the container runs and is not paused. When the
  container does not run, the upgrade does no check, and the keychain item
  stays. Then the step tells you to start the server and do the upgrade
  again. The new container gets the port and
  the data folder of the container that it replaces. The upgrade examines
  them before it stops that container. It does not write the image of a
  different instance to `config.json`.
- `mai uninstall` ends the leases on the port of the container of the
  instance. When that container does not run, it sends no key, and the
  remove of the container ends the leases. The size of the mail data counts
  only the volumes and the data folders of the instance.
- When `config.json` names a different instance, each step of a command
  names `--instance NAME`.

## Bridge

`mai mcp --agent A` gets the pool lease of the agent when it starts. Then it
gets one lease for each named session that a chat uses. The bridge reads the
agent key from the key file at each lease request. Thus a new key from
`mai agent rotate` applies at the next lease request. When the key file has
no key for the agent, or the server refuses the agent key, the result is
`BAD_KEY`. The message gives the step: `mai agent rotate NAME` for an agent
that exists on the server, or `mai agent add NAME` for a new agent.

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
   the result of that request: the lease or the tool error. The same rule
   applies to the new lease after a restart of the server, for a named
   session and for the pool lease.
3. When the lease request is not successful, the call gets the tool error of
   `login` with the path `session`. Examples are `BAD_LABEL` for a label of
   the pool, `SESSION_BUSY`, and `SERVER_UNAVAILABLE`. When the key file has
   no key for the agent, or the server refuses the agent key, the code is
   `BAD_KEY`. When the bridge cannot read the key file, the code is
   `KEY_FILE_ERROR`. A refusal keeps no lease, thus the next call sends a new
   lease request.
4. A `login` to a session that the bridge holds gives the handle of that
   lease, also when a call with the label got the lease.
5. A `logout` with the label, the login, or the handle ends the lease of the
   named session. The next call with the label gets a new lease.

The bridge removes the argument `session` before it sends the call. A value
with the form of a handle is always a handle. A value that is not a label, a
login, or a handle gives `INVALID_ARGUMENT`, for example an empty string. A
call with no argument `session`, or with `null`, uses the pool lease. The notice file and `mai hook`
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
When the server gives no code, the code is `SERVER_UNAVAILABLE`. The tool
error has `retry_safe` true. The next call or heartbeat sends the lease
request again.

A call with the argument `session` gets the path `session`. A call with no
`session` uses the pool lease, and its tool error has no path.

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
from the lease that the logout ends.

When a logout ends the lease during a call with the label, the server does
not do the call. The bridge then gets a new lease for the session and sends
the call again. This is also true when the call waits for the new lease
after a restart of the server. A call with the handle of the session gets
`SESSION_UNKNOWN` with `retry_safe` false, because the logout ended the
handle.

The bridge stops when the app closes the stdin of the bridge. It also stops
when a write to its stdout is not successful, for example after the app
exits during a call. On Unix, the bridge also stops on SIGINT, SIGTERM, or
SIGHUP. On Windows, it stops on Ctrl+C or when the console closes.

After the end of stdin, the bridge waits a maximum of 10 seconds for the
results of the calls in flight. On Unix, the bridge does not wait when the
app is gone, because nothing can read the results. The app is gone when its
process ended, or when it closed the read end of the stdout of the bridge.
Then the bridge stops each call in flight immediately.

Before the bridge stops, it ends each lease and removes its notice file. The
bridge ignores SIGPIPE. Thus a closed stdout or stderr does not stop the
bridge before this step. The bridge ends the leases at the same time, and it
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

The CLI cannot use the owner API when the server runs, but the key file has
no owner key or the server refuses it. For example, the CLI uses a different
`MAI_HOME` with a different key file. Then use the `MAI_HOME` with the owner
key of the server. To give the server the owner key of this key file, do
these steps:

1. If the key file has no owner key, run `mai start`. It makes a new owner
   key in the key file. The server refuses it, because the server has an
   owner key.
2. Run `mai exec -- mai owner set-key --replace`, and type the owner key of
   the key file on stdin. The server then accepts only that owner key.

### Keys of mai 3.0 and 3.1

mai 3.0 and 3.1 kept the keys in the system keychain, with the service
`dev.mai` and the same account names.

- When the key file has no key for an account, the CLI reads the keychain item
  of the account. It writes the key to the key file, and then it deletes the
  keychain item. On macOS, the read can show one prompt for each item. The
  CLI reads the items of each build of mai 3.0 and 3.1, with cgo or with no
  cgo.
- When no keychain is available and the manifest records a keychain item for
  the account, the command gives an error with a step. Thus `mai start` does
  not make a new owner key in place of the key of the keychain item. The
  versions 3.0 and 3.1 did not operate with no keychain. Thus with no record,
  no item exists.
- `mai upgrade` moves all the keys of the instance. When the key file has the
  same key for the account of a keychain item, `mai upgrade` deletes the
  keychain item. A keychain item stays when the key file has a different key
  for its account, or when the CLI cannot delete the item. Then
  `mai upgrade` gives a step and the exit code 1. For the owner key,
  `mai upgrade` does a test of the two keys on the server. When the server
  accepts only the key of the key file, `mai upgrade` deletes the keychain
  item. When the server accepts only the key of the keychain item,
  `mai upgrade` writes that key to the key file and deletes the keychain
  item. Thus no command deletes or replaces a key that the server accepts.
- After `mai upgrade`, start each app that runs `mai mcp` again, or start its
  MCP server again. `mai upgrade` also gives this step when it deletes the
  keychain item of an agent. It also gives the step when the manifest records
  the keychain item of an agent, and the item is gone. For example, a bridge
  of 3.2 moves its agent key to the key file at its first lease request. A
  bridge of mai 3.0 or 3.1 that runs reads the agent key from the keychain
  item at each lease request. When the item is gone, the bridge stops with
  `BAD_KEY` and the step `Run mai agent add NAME`. Do not do that step,
  because `mai agent add` gives `AGENT_EXISTS` for an agent that exists.
  Start the app again.
- `mai agent add NAME` and `mai agent rotate NAME` delete the keychain item of
  the agent, because the server then accepts only the new key.
- `mai doctor` tells about each keychain item of the instance. When no
  keychain is available and the manifest records keychain items of the
  instance, `mai doctor` gives a warning with a step.
- `mai uninstall` removes the keys of the instance from the key file and each
  keychain item of the instance. When a keychain item of the instance stays,
  for example because no keychain is available, `mai uninstall` gives a step
  and the exit code 1. Then the manifest stays with the record of each such
  item, and the binary also stays. Thus a subsequent `mai uninstall` can
  remove the items. Each step gives `mai uninstall --instance NAME`, unless
  the instance is `mai` and `config.json` names it. A command with no
  `--instance` acts on the instance of `config.json`, and that file can stay
  for a different instance.
- Instances in the same state folder use one key file and one manifest. Each
  record of the manifest has its instance. `mai uninstall --instance NAME`
  removes only the items of that instance. A container or a volume goes only
  when its label `dev.mai.instance` is that instance. An image that a
  container of a different instance uses stays, and its record goes to that
  instance. Thus the uninstall of the last instance removes the image. The
  state folder can have no keys and no records of that instance, for example
  for a container of a CLI with a different `MAI_HOME`. Then the image stays
  with no record, and the report tells this. A
  record of mai 3.0 or 3.1 of an app entry or of a file of an app has no
  instance. The entry also names no instance. Each uninstall keeps such
  an entry while a different instance stays, and tells it. The uninstall of
  the last instance removes it. The record of a data
  folder of mai 3.0 or 3.1 has no instance. The container with the folder as
  its mount at `/data`, or `config.json` with that `data_dir`, tells its
  instance. When no instance is known, the folder stays. The uninstall never
  removes a folder that a container of a different instance mounts. The
  data folder of a different instance also stays when no container has it
  as a mount. A data folder that stays is kept also when it is in a folder of the
  CLI, for example in the configuration folder. While a
  different instance has keys in the key file or records in the manifest,
  some files stay. The key file stays, and the manifest stays with the
  records of the other instances. The configuration file of such an instance
  and the binary also stay. The entries that `mai connect` wrote in the files
  of the apps also stay, because an entry names no instance. Its bridge uses
  the instance of `config.json`. When `config.json` goes with the instance,
  the report gives a line for each entry that stays, also for an entry of a
  different instance. That line gives the step `mai start --instance NAME`.
  It makes NAME the instance of `config.json`, with the port of its
  container.
  Then run `mai uninstall --instance NAME` for
  each of these instances. The uninstall of the last instance removes the
  entries. It removes the key file only when the file holds no key. It holds
  the lock of the file for the check and the remove. Thus a key that a
  different process writes during the uninstall stays.
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

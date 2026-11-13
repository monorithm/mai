# Changelog

All notable changes to this project are in this file. The format is
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and the project
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The key file of the host CLI. The file `keys.json` in the state folder holds
  the owner key and the agent keys of each instance, with the account names of
  the keychain. The file has the mode 0600, in a folder with the mode 0700.
  Each write is atomic, and a lock lets one process at a time read or change
  the file. Thus two CLIs or bridges at the same time do not lose a key. On
  Unix, the CLI refuses a key file that a group or other users can read, and
  the error gives the `chmod` command.
- The move of the keys of 3.0 and 3.1. When the key file has no key for an
  account, the CLI reads the keychain item of the service `dev.mai`. It writes
  the key to the key file, and then it deletes the keychain item.
  `mai upgrade` moves all the keys of the instance. When no keychain is
  available and the manifest records a keychain item for the account, the
  command gives an error with a step. Thus `mai start` makes no new owner key
  in place of the key of the keychain item. A keychain item stays when the key
  file has a different key for its account, or when the CLI cannot delete
  the item. Then `mai upgrade` gives a step and the exit code 1. For the owner
  key, `mai upgrade` does a test of the two keys on the server. When the
  server accepts only the key of the key file, `mai upgrade` deletes the
  keychain item. When the server accepts only the key of the keychain item,
  `mai upgrade` writes that key to the key file and deletes the keychain
  item. `mai agent rotate` also deletes the keychain item of the agent.
- After `mai upgrade`, start each app that runs `mai mcp` again, or start
  its MCP server again. `mai upgrade` also gives this step when it deletes
  the keychain item of an agent. It also gives the step when the manifest
  records the keychain item of an agent, and the item is gone. For example,
  a bridge of 3.2 moves its agent key to the key file at its first lease
  request. A bridge of 3.0 or 3.1 that runs reads the agent key from the
  keychain item at each lease request. When the item is gone, the bridge
  stops with `BAD_KEY` and the step `Run mai agent add NAME`. Do not do that
  step, because `mai agent add` gives `AGENT_EXISTS` for an agent that
  exists. Start the app again.
- `mai doctor` examines the key file and tells about each keychain item of the
  instance. It shows no key. On macOS, it reads no key and shows no prompt.
  When no keychain is available and the manifest records keychain items of
  the instance, `mai doctor` gives a warning with a step.
- MCP contract 3.2.0: the argument `session` of each tool other than `login`
  and `logout` takes the label of a named session, for example `lane-1`. It
  also takes the login of the session, for example `cursor.lane-1`, or a
  handle, as before. A chat that gives the label in each call does not call
  `login`. The tool `logout` also takes a label or a login.
- The bridge `mai mcp` gets the lease of a named session at the first call
  with its label or its login. Each subsequent call with the same label,
  login, or handle uses the same lease. Calls at the same time for the same
  session make one lease request, and each of these calls gets the result of
  that request. The same rule applies to the new lease after a restart of the
  server, for a named session and for the pool lease. A `login` to that
  session gives the handle of the same lease.
  When the lease request is not successful, the call gets the tool error of
  `login` with the path `session`. Examples are `BAD_LABEL` for a label of
  the pool, and `SESSION_BUSY`. A value that is not a label, a login, or a
  handle gives `INVALID_ARGUMENT`.
- The code `KEY_FILE_ERROR` of the bridge. The bridge gives it when it cannot
  read the agent key from the key file or from its keychain item. A tool call
  or a `login` then gets this code with `retry_safe` false. The message gives
  the step, for example the `chmod` command. A call with the argument
  `session` gets the path `session`, and a call with no `session` gets no
  path. When a different process holds the lock of the key file for more than
  10 seconds, the code is `BUSY` with `retry_safe` true. Before, these errors
  gave `SERVER_UNAVAILABLE` with `retry_safe` true.

### Changed

- The bridge reads the agent key from the key file at each lease request.
  Thus a new key from `mai agent rotate` applies to a bridge that runs. When
  the key file has no key for the agent, the result is `BAD_KEY`, as before.
  The message gives the step `mai agent rotate NAME` for an agent that exists
  on the server, and `mai agent add NAME` for a new agent. For an agent that
  exists, `mai agent add` gives `AGENT_EXISTS`. When the server refuses the
  agent key of the key file, the code is also `BAD_KEY`, and the message gives
  the same steps. The tool error, the stop of the bridge, and the log of the
  bridge give these steps.
- `mai uninstall` removes the keys of the instance from the key file and each
  keychain item of the instance. While a different instance has keys in the
  key file or records in the manifest, some files of the CLI stay. The key
  file stays, and the manifest stays with the records of the other
  instances. The configuration file of such an instance and the binary also
  stay. The entries that `mai connect` wrote in the files of the apps also
  stay, because an entry names no instance. Its bridge uses the instance of
  `config.json`. The uninstall of the last instance removes these entries.
  When a keychain item of the instance stays, for example because no
  keychain is available, `mai uninstall` gives a step and the exit code 1.
  Then the manifest stays with the record of each such item, and the binary
  also stays. Thus a subsequent `mai uninstall` can remove the items.
- With `MAI_HOME`, the CLI keeps all its files in that folder, and it uses no
  keychain item.
- On macOS, cgo is necessary only for the Security framework, which reads and
  removes the keychain items of 3.0 and 3.1.
- A tool call that comes to the server with the argument `session` gives
  `BRIDGE_ONLY` with the path `session`, and the tool does not run. In 3.1,
  the server removed the argument, and the call acted as the session of its
  lease, for example a pool session.
- The instructions of `initialize` and the guide `mai://guide` tell a chat to
  give the label of its session as `session` in each call. The message of
  `NO_FREE_SESSION` also tells this rule. The error table of the guide also
  lists the codes `BAD_KEY` and `KEY_FILE_ERROR` of the bridge.
- `login` uses the value of `session` as it is, with the rule of a tool
  call. A value that is not a label or a login gives `BAD_LABEL` with the
  path `session`, and no lease request. An example is a label with a space
  or a line break at the end. Before, `login` removed the spaces and the
  line breaks at the start and at the end of the value. A `logout` with no
  `session`, an empty string, or `null` gives `INVALID_ARGUMENT` with the
  path `session`. Before, it gave `SESSION_UNKNOWN`.
- `scripts/it_named_sessions.sh` builds mai with no tag and uses only a
  scratch `MAI_HOME`. It does a test of calls with the label `lane-c` and no
  login. At the end, it makes sure that `mai uninstall` removed the keys of the
  instance from the key file.
- `scripts/it_leases.sh` does a test of `BRIDGE_ONLY` with the path `session`
  for a handle, a label, and a login.

### Removed

- The presence check before the first lease of each bridge, `mai ui open`,
  and `mai exec`: Touch ID, Windows Hello, and polkit. On a self-run instance,
  a program that can use the Docker socket controls mai. Thus the keychain and
  the prompts gave friction and no protection.
- The command `mai approvals`, its file `approvals.json`, and the field
  `approval` of `config.json`.
- The build tag `presencefake`, the variable `MAI_TEST_KEYCHAIN_SERVICE`, the
  polkit action `dev.mai.lease`, and the CI step
  `go test -tags presencefake ./...`.

### Fixed

- `mai uninstall --instance NAME` ends the leases on the port of the
  container of that instance. When the container does not run, the
  uninstall sends no key and tells this in a line. Then the remove of the
  container ends the leases. Before, the uninstall sent the owner key of the
  instance to the port of `config.json`, which can be the server of a
  different instance. The size of the mail data also counts only the
  volumes and the data folders of the instance.
- `mai status --instance NAME` and `mai doctor --instance NAME` use the port
  of the container of that instance. Before, they used the port of the
  instance of `config.json`. Thus `mai status` showed the port and the
  version of a different server, and it sent the owner key of the instance
  to that server.
- `mai upgrade --instance NAME` does the check of the owner keys on the port
  of the container of that instance. When that port is not known, it does no
  check. The new container gets the port and the data folder of the
  container that it replaces. The upgrade examines them before it stops that
  container. Before, the upgrade used the values of the instance of
  `config.json`. Thus it sent the owner keys of the instance to a different
  server. It also removed the container of the instance, and then it stopped
  because a different server used the port. When `config.json` names a different instance, each step
  names `--instance NAME`, and the upgrade does not change `config.json`.
- `mai start --instance NAME` uses the port and the data folder of that
  instance, also when `config.json` names a different instance. Before, the
  start took the port and the data folder of the instance of `config.json`.
  Thus two containers could use one data folder. Then `config.json` named
  the new instance with the port of the other instance. Thus the subsequent
  commands sent the keys of the new instance to the other server. The start
  also refuses a data folder that a container of a different instance
  mounts, and `config.json` changes only after a successful start.
- The bridge ends each lease and removes its notice file when the app exits
  during a call, for example during `wait_for_mail`. Before, the write of
  the result to the closed stdout or stderr stopped the bridge with the
  signal SIGPIPE. Thus the server kept each lease of the bridge until the
  lease expired, and a new bridge got `SESSION_BUSY` for the same named
  session. The bridge also stops when a write to its stdout is not
  successful.
- `mai uninstall --instance NAME` removes only the items of that instance.
  Each record of the manifest has its instance. The CLI gives each record of
  3.0 and 3.1 the instance of its name, or the instance of the file. A
  container or a volume goes only when its label `dev.mai.instance` is the
  instance. Before, the uninstall of one instance also removed some items of
  the other instances in the same state folder. These items were the
  container, the volumes, the images, and the app entries. It also removed
  the records of the keychain items of these instances. Thus `mai upgrade`,
  `mai doctor`, and `mai uninstall` of such an instance did not find its
  keychain items.
- The record of a data folder of 3.0 or 3.1 does not get the instance of the
  file. Its instance is the instance of the container with the folder as its
  mount at `/data`, or the instance of `config.json` with that `data_dir`.
  When no instance is known, the folder stays, and `mai uninstall` tells
  this. `mai uninstall` never removes a folder that a container of a
  different instance mounts. Before, the uninstall of the instance of the
  file removed the data folder of a different instance.
- When the bridge gets no new pool lease, a tool call with no argument
  `session` gets a tool error with no path. Examples are `MAIL_SERVER_DOWN`
  and `NO_FREE_SESSION`. Before, the tool error had the path `session`,
  although the call had no such argument. The server gives `NO_FREE_SESSION`
  with no path for the same call.
- The server refuses a value with a line break at the end for an argument
  with a pattern. Examples are a `ref`, a `thread_ref`, and an address. Before,
  the validator of the server used the Python rule for `$`, which also matches
  before a line break at the end. A strict MCP client uses the ECMA-262 rule,
  and it refused such a value.
- Each output schema compiles in the strict mode of ajv. In 3.1.1, each
  alternative of the 12 output schemas with the keyword `required` had only
  `required`, and the properties were only in the parent schema. Ajv with
  `strict: true` refused these schemas. Thus an MCP client with such a
  validator could not list the tools. With this change, each alternative
  also has the properties of the fields in its keyword `required`. The
  schemas accept and refuse the same results as before.

## [3.1.1] - 2026-11-13

### Fixed

- A tool error matches the output schema of its tool. A strict MCP client,
  for example the client of Cursor, compares each tool result with the output
  schema, also an error result. In 3.1.0 and 3.0.x, the client refused each
  error of the 12 tools whose output schema has the keyword `required`. The
  chat then got the
  client error `-32602` in place of the code of the error. The output schema
  of each tool accepts the form `{"error": {...}}` as an alternative to the
  fields of a successful result. The MCP contract is 3.1.1.
- The argument `session` and the guide `mai://guide` tell a chat to give the
  handle of its login in each call, also in `reply` and `read_message`. A call
  with no `session` acts as the pool session, which does not have the mail of
  the named session. Thus such a call gives `UNKNOWN_MESSAGE`.
- A summary of a mail with no `Date` header, a `Date` header that does not
  parse, or no `From` address matches the output schema. The fields `date`
  and `from` can be `null`. In 3.1.0, a strict client refused each
  `list_inbox`, `read_message`, `read_thread`, and `wait_for_mail` result
  with such a mail, for example a mail from `mai import-maildir`.
- `claim_thread` finds a queue mail that arrived after the last command of the
  session. A second select of the selected mailbox sends `NOOP`.
  Thus Dovecot syncs the view of the session before the search. In 3.1.0,
  the first `claim_thread` gave `UNKNOWN_MESSAGE`, and only a second call
  found the mail.

## [3.1.0] - 2026-11-13

### Added

- Named sessions as in 2.x, with no session secret on the host. The owner API
  `POST /api/sessions` makes a named session `AGENT.LABEL`, or with `count`
  the pool sessions `AGENT.s1` to `AGENT.sN`. `POST /api/sessions/LOGIN/rotate`
  gives a session a new internal secret, and `POST /api/sessions/LOGIN/revoke`
  closes it for good. Each of the two ends the lease of the session.
  `GET /api/sessions` shows the status and the lease of each session. The
  owner API gives no session secret.
- A lease request can name a session of the agent in the field `session`, for
  example `lane-a` or `cursor.lane-a`. The server refuses a session of a
  different agent (`SESSION_UNKNOWN`), a revoked session (`SESSION_REVOKED`),
  and a session that a different lease holds (`SESSION_BUSY`). A lease request
  with no name gets a pool session, and never a named session.
- MCP contract 3.1.0: the tools `login` and `logout` of the bridge, and the
  optional argument `session` of each other tool. The argument is a handle:
  `ses-` and 16 base32 characters. The server removes the argument before the
  tool runs. A call of `login` or `logout` that comes to the server gives the
  error `BRIDGE_ONLY`.
- The host CLI commands `mai session new AGENT --label L`,
  `mai session new AGENT --count N`, `mai session rotate LOGIN`, and
  `mai session revoke LOGIN` as in 2.x. Each command shows only the login.
  For `mai session revoke`, type `revoke` to agree, or use `--yes`.
- The bridge `mai mcp` does the tools `login` and `logout`. A login gives a
  handle `ses-...` that is valid only in that bridge. A tool call with the
  handle as `session` acts as that named session. A tool call with no
  `session` uses the pool session of the bridge. The presence check occurs
  one time for each bridge process.
- `mai hook` shows one line for each session of its bridge that has new mail.
- The first tool call on a lease with no session gives the lease a free pool
  session. When no pool session is free, the tool result is the error
  `NO_FREE_SESSION` with `retry_safe` true and HTTP 200.
- The tool error code `NO_FREE_SESSION`. The guide `mai://guide` tells the
  steps for a call that gets it.

### Changed

- The server CLI commands `session new AGENT --label L`,
  `session new AGENT --count N`, `session rotate`, and `session revoke` show
  only the login. `session list` shows the login, the status, and the time of
  the last login. The flags `--file`, `--credential-only`, and `--expires` go
  away.
- `mai session list` on the host shows the label and the state of each
  session, and the app, the folder, and the process ID of each lease.
- When no pool session of the agent is free, a lease request with no session
  gives a lease with the session `null`. Each agent can have 16 such leases.
  After that limit, the lease request gives `NO_FREE_SESSION`.
- `scripts/it_container.sh` and the milter container test take the image from
  `MAI_IT_IMAGE` (default `mai:dev`).
- `scripts/it_named_sessions.sh` waits for each lease state. It also does a
  test of the pool label rule and of a bridge with a full pool.

### Fixed

- A send that occurs more than 300 s after the login of a session delivers the
  mail. Postfix closes an SMTP connection that has no command for its
  `smtpd_timeout` (300 s): it logs `timeout after AUTH` and sends 421. The app
  read the 421 as a refused sender and did not connect again. A 421 reply gets
  a new connection and one more send, as a lost connection does.
- After a failed send, a retry of `send_message` or `reply` with the same
  `request_id` delivers the mail one time. In 3.0.1, the app role had no right
  to delete the retry key of the failed send. Thus the call gave `INTERNAL` in
  place of the error of the send. Also, the retry gave a duplicate of a mail
  that did not go out. The retry key has a state (`pending`, `sent`, or
  `failed`). The app changes the state and does not delete the key.
- A duplicate of `send_message` or `reply` gives the `date` of the sent mail.
  In 3.0.1, `date` was `null`, and an MCP client refused the result because
  the output schema has `date` as a string.
- The migration 420 gives a state to each retry key from 3.0.1. A key whose
  mail has an envelope record gets the state `sent`. Each other key gets the
  state `failed`, thus a retry of the key sends the mail.
- `DELETE /api/agents/{name}` removes the agent. In 3.0.1, the removal deleted
  the row of the agent key, but the app role has no DELETE right. Thus the
  request gave HTTP 500 and the agent stayed. The removal keeps the row and
  removes the hash of the key from it. The migration 211 lets the column
  `key_hash` have no value.
- The owner API and the server CLI refuse a named session with a label of the
  pool (`s` and digits, for example `s1`). The result is `BAD_LABEL`, and the
  message tells the owner to use `--count`.
- A lease request for a label of the pool, for example `s2` or `cursor.s2`,
  gives `BAD_LABEL`. Thus a login never takes a pool session, and the pool
  never gives a named session.
- Each error of a lease request has a code. A failure of the mail login gives
  its code: 503 for `MAIL_SERVER_DOWN` and `STORE_UNAVAILABLE`, 502 for each
  other code, with `retry_safe`. A revoke during the lease request gives
  `SESSION_REVOKED`. Other failures give a coded 500 `INTERNAL`.
- A store failure during the check of the agent key gives
  `STORE_UNAVAILABLE`, not `BAD_KEY`.
- A heartbeat gives `"new_mail": null` when the count of new mail fails, and a
  coded 503 when the store does not reply. It never gives a 500 with no code.
- The server refuses a label, an agent name, a login, or a lease token with
  a line break at the end. Before, the owner API and the server CLI made
  the named session `cursor.s1\n`, and a lease request for `s2\n` gave
  `SESSION_UNKNOWN`, not `BAD_LABEL`.
- A lease with no session ends when the owner removes its agent. Its
  heartbeat and its tool calls give HTTP 401 `LEASE_UNKNOWN`. Thus the bridge
  renews the lease and gets `BAD_KEY`. Before, the heartbeat gave 200 and each
  tool call gave `NO_FREE_SESSION` with `retry_safe` true.
- When a revoke closes the selected pool session during a bind or a pool lease
  request, the server tries the next free pool session. Thus
  `NO_FREE_SESSION` occurs only when no pool session is free.
- The bridge `mai mcp` starts and serves named sessions when no pool session
  of the agent is free. It accepts a pool lease with no session, and logs
  that a chat can log in to a named session. When a heartbeat gives the pool
  session, the bridge records it.
- A server error, an error with no code, or a network error of a lease
  request does not end a named session in the bridge. The call gets a tool
  error with the code of the server, or `SERVER_UNAVAILABLE`, with
  `retry_safe` true. The next call or heartbeat tries again.
- A failed renew of the pool lease stops the bridge only when the agent key is
  not valid (`BAD_KEY`). For each other failure, the named sessions stay.
- A login to a session waits for a logout of the same session in the same
  bridge. Thus the login does not get `SESSION_BUSY` from the lease that the
  logout ends.
- A login whose lease request gets a server error gives a tool error that the
  chat can try again, not a JSON-RPC error.
- The bridge ends each lease and removes its notice file on SIGHUP, the same
  as on SIGINT and SIGTERM.
- While the pool lease has no session, the notice file has no pool entry.
  `mai hook` shows the named sessions of such a notice.
- A heartbeat with no count of new mail (`"new_mail": null`) keeps the last
  count in the notice file.
- `mai session new AGENT --label s1` gives `BAD_LABEL` before it calls the
  server. A label `s` with only digits after it is for the pool. Use
  `--count N`.
- The plan of `mai uninstall` shows the keychain service from which it deletes
  the keys, also in a test build with `MAI_TEST_KEYCHAIN_SERVICE`.
- The bridge ends the lease of a lease request that a logout or a stop
  interrupts. A login to the same session then does not get `SESSION_BUSY`,
  and no lease stays on the server after a stop.
- The bridge writes the notice file each time it gets a new lease, also when a
  tool call gets the new lease.
- A login that gets an HTTP 5xx error gives `retry_safe` true, the same as a
  tool call with a handle.
- A revoked pool session does not hold a place in the pool. The pool has a
  maximum of 8 open pool sessions, and a new pool session gets the lowest
  free number, for example `cursor.s9`. Before, an agent that the owner
  removed and added again got no pool session, because its revoked sessions
  `s1` to `s8` filled the pool.
- Each lease request, tool call, and heartbeat has a time limit. The limit is
  20 s for a lease request and for the lease steps of a tool call. It is 10 s
  for a heartbeat. Before, a mail server or a store that did not reply gave
  HTTP 504 with no code after 30 s. The error has the code of the part that
  ran:
  `MAIL_SERVER_DOWN`, `STORE_UNAVAILABLE`, or `BUSY`, with HTTP 503 and
  `retry_safe` true. Each limit is less than the waits of the bridge, thus the
  bridge gets the code.
- A count of new mail that takes more than 5 s gives `"new_mail": null`. A
  tool call that holds the session does not stop the heartbeat.
- A revoke that comes after the new secret of a session, before its mail
  login, gives `SESSION_REVOKED`. A pool lease request or a bind then tries
  the next free pool session. Before, the result was `CREDENTIAL_REVOKED` with
  `retry_safe` false.
- After a tool call on a pool lease with no session, the bridge sends a
  heartbeat immediately. Thus the notice file gets the pool entry of a bind
  before the next heartbeat.
- A 552 reply of Postfix to the data gives `BODY_TOO_LARGE`. Before, the app
  connected again and sent the mail a second time.
- A pool lease request runs doveadm only when it makes a pool session. A
  doveadm command stops after 10 s.
- A `wait_for_mail` with `timeout_sec` more than 30 gives its result. In
  3.0.1, the request limit of the HTTP server (30 s) stopped each wait with
  no new mail. The call then got HTTP 504 with no code. The request limit is
  75 s, more than the longest wait (50 s).
- A lease request or a bind at the time of an agent removal does not make a
  new pool session for the removed agent. A lease request gives `BAD_KEY`, and
  a bind ends the lease with `LEASE_UNKNOWN`. The credentials refuse a new
  session of an agent that is not active. Each heartbeat and each tool call
  examines the agent of the lease. Thus a lease of a removed agent ends at its
  next heartbeat or tool call, and the server revokes its session.
- An owner revoke that comes during the secret check of a login stays. In
  3.0.1, the login set the status `active` again, and the revoked session got
  new leases. The login then gives `CREDENTIAL_REVOKED`.
- A lease request ends the expired leases at the same time. Before, the
  logouts ran one after the other, and many expired leases made the request
  go past its time limit.
- The end of a lease closes its mail connections also when the store does
  not reply or fails.
- A revoke during the IMAP login of a lease request gives `SESSION_REVOKED`,
  and the pool tries the next free pool session. Before, the result was
  `LOGIN_FAILED` with `retry_safe` false. After an owner rotate of the
  selected pool session, the pool tries one different free session.
- A store that does not reply during the mail login gives
  `STORE_UNAVAILABLE`. Only the connection to the mail server and doveadm
  give `MAIL_SERVER_DOWN`.
- At a stop, the bridge ends its leases at the same time, and it waits a
  maximum of 10 s for each lease. Before, all the leases had one wait of 5 s,
  and a slow end of one lease kept the other leases on the server.

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
- `mai disconnect` removes the empty folders that `mai connect` made for a file,
  also a folder for two files. A link of the user in the place of such a folder
  stays.
- A dotfile that the user links after `mai connect` keeps its own containers
  and its `version`. A file that the user moves into a dotfiles folder and links
  (`stow --adopt`) loses the empty containers that mai made.
- `mai connect codex` writes the files in `CODEX_HOME` when it is set.
- A second `mai connect codex` keeps an `args` array on more than one line, with
  its comments, when the values do not change. A string that `toml_edit` writes
  in a different form (`'C:\Users\...'`) counts as the same value.
- After `codex mcp remove mai`, `mai disconnect codex` removes the end comment of
  3.0.0.
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

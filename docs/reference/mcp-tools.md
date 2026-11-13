# Reference: MCP tools

The server name is `mai`. The contract version is `3.1.0`.

On HTTP, `tools/list` gives all 22 tools. For each tool call, the lease token
of the bridge `mai mcp` is necessary as the bearer token.

## Sessions

1. A tool call with no `session` argument uses the pool session of the bridge,
   for example `cursor.s1`.
2. The tool `login` takes the label or the login of a named session of the
   agent of the bridge, for example `lane-a`. The bridge gets a lease on that
   session and gives a handle, for example `ses-abcdefghijklmnop`.
3. Each other tool has the optional argument `session`. With a handle, the
   bridge sends the call with the lease of that handle. The server removes the
   argument before the tool runs.
4. The tool `logout` takes a handle and ends its lease.

The bridge answers `login` and `logout` itself. A call of these tools that
comes to the server gives the error `BRIDGE_ONLY`.

### Pool labels

A label that agrees with `^s[0-9]+$`, for example `s1`, `s9`, or `s01`, is a
label of the pool. Only the pool and `mai session new AGENT --count N` make
the sessions with these labels.

- The owner request for a named session with a label of the pool gives
  `BAD_LABEL`. The message tells the owner to use `--count`.
- A `login` or a lease request with a label of the pool, for example `s2` or
  `cursor.s2`, gives `BAD_LABEL`. A chat that calls no `login` uses the pool.
- A label, an agent name, or a login with a line break at the end is not
  valid. For example, the owner request for the label `s1` with a line break
  gives `BAD_LABEL`.

Thus a `login` never takes a pool session, and the pool never gives a named
session.

### A lease with no session

The pool of an agent has 8 sessions. A revoked pool session never gets a lease again, thus it does not hold a
place in the pool. When the
pool has less than 8 open pool sessions, the server can make a new pool
session. The new pool session gets the lowest free number, for example
`cursor.s9` after a revoke of `cursor.s3`.

When no pool session is free, the lease
request of the bridge gets a lease with no session. In the result, the field
`session` is `null`. Each agent can have 16 leases with no session. After
that limit, the lease request gives `NO_FREE_SESSION`.

| Call on a lease with no session | Result |
|---|---|
| `initialize`, `ping`, the notifications, `tools/list`, `resources/list`, `resources/read`, and `prompts/*` | The usual result. A session is not necessary for these methods. |
| A heartbeat | `{"session": null, "new_mail": 0}` |
| A tool call | The server gives the lease a free pool session, then the tool runs as that session. Each subsequent heartbeat gives that session. |
| A tool call when no pool session is free | A tool error `NO_FREE_SESSION` with `retry_safe` true, and HTTP 200. The lease stays. |
| A tool call or a heartbeat after the owner removes the agent | HTTP 401 `LEASE_UNKNOWN`. The lease ends. The bridge then sends a new lease request and gets `BAD_KEY`. |

If the owner revokes the selected pool session during the tool call, the
server tries the next free pool session. Thus `NO_FREE_SESSION` occurs only
when no pool session is free.

The owner list of the leases shows a lease with no session with the session
`null`. The owner list of the sessions has one row for each session, thus it
does not show that lease. The commands `mai session end`, `mai session
rotate`, and `mai session revoke` act only on the leases of that session.

### Lease errors

Each error of a lease request has the same JSON form as the other errors of
the lease API: `{"error": {"code": ..., "message": ...}}`.

| Cause | Status | Code |
|---|---|---|
| The mail server or the store does not reply during the mail login of the session | 503 | `MAIL_SERVER_DOWN` or `STORE_UNAVAILABLE`, with `retry_safe` true |
| A different failure of the mail login | 502 | The code of the failure, for example `LOGIN_FAILED`, with `retry_safe` |
| The owner revokes the session during the lease request | 409 | `SESSION_REVOKED` |
| The session does not exist | 404 | `SESSION_UNKNOWN` |
| A lease request takes more than its time limit (20 s) | 503 | The code of the part that runs: `MAIL_SERVER_DOWN`, `STORE_UNAVAILABLE`, or `BUSY` when the request waits for a different lease request. `retry_safe` is true. |
| A heartbeat takes more than its time limit (10 s) | 503 | `MAIL_SERVER_DOWN` or `STORE_UNAVAILABLE`, with `retry_safe` true. The lease stays. |
| An internal error | 500 | `INTERNAL` |

Each time limit of the server is less than the request limit of the HTTP
server (75 s). It is also less than the wait of the bridge: 30 s for a lease
request and 15 s for a heartbeat. Thus the bridge gets the code of each
failure. A tool
call on a lease has the time limit of a lease request for the steps of the
lease. If the server cannot count the new mail in 5 s, the heartbeat gives
`"new_mail": null`.

The request limit of the HTTP server (75 s) is more than the longest wait of
`wait_for_mail` (50 s). Thus a wait with no new mail gives the result
`timed_out` true, not an HTTP error. A heartbeat never gives an error with no code.

## Resources

The server has the capability `resources`. A client can read the resources with no
login, on stdio and on HTTP, because they have only text of the server and no mail.

| URI | Content |
|---|---|
| `mai://guide` | How an agent uses mai: login, mail, threads, the board, errors, and the trust rules. The guide ends with a table of the tools, which the server makes from the tool schemas. |

- `resources/list` gives the list.
- `resources/read` with `uri` gives the text, with the type `text/markdown`.
- An unknown URI gives the error `-32002` "Resource not found".
- `resources/templates/list` gives an empty list.

## Tools

| Group | Tools | Roles |
|---|---|---|
| Session | `login`, `logout`, `whoami` | All |
| Mail | `list_addresses`, `send_message`, `reply`, `list_inbox`, `read_message`, `read_thread`, `wait_for_mail`, `mark_done` | All |
| Threads | `claim_thread`, `release_thread` | All |
| Work | `get_board`, `read_item`, `start_item`, `report_status`, `submit_for_review` | worker, supervisor, reviewer |
| Supervision | `plan_item`, `assign_item`, `review_item`, `manage_sprint` | supervisor |

`list_addresses` gives the agents of the store and their live sessions. The
command `mai agent add` adds an agent. The code of the server has no list of agent names.

## Shared types

| Type | Form | Example |
|---|---|---|
| `address` | An agent address or a session address in `mai.internal` | `cursor.lane-a@mai.internal` |
| `ref` | `ref-` and 16 hex digits | `ref-3f9a0c1d2b4e5f60` |
| `thread_ref` | `thr-` and 16 hex digits | `thr-a1b2c3d4e5f60718` |
| `item_ref` | `itm-` and 12 base32 characters | `itm-k3q9x2m7p4ta` |
| `session` | A handle of the bridge: `ses-` and 16 base32 characters (`a` to `z`, `2` to `7`) | `ses-abcdefghijklmnop` |
| `kind` | `request`, `reply`, `status`, `notice`, `signal` | `request` |
| `priority` | `low`, `normal`, `high` | `normal` |
| `status` | `unread`, `notified`, `seen`, `done` | `seen` |
| `date` | UTC, `YYYY-MM-DDTHH:MM:SSZ` | `2026-11-12T09:30:00Z` |

## Error codes

A tool error has a `code`. Some errors also have a `path` (the argument that
has the error).

| Code | Meaning |
|---|---|
| `INVALID_ARGUMENT` | An argument is not correct. |
| `LOGIN_FAILED` | The credential is not correct. |
| `CREDENTIAL_EXPIRED` | The credential is expired. |
| `CREDENTIAL_REVOKED` | The credential is revoked. |
| `SESSION_UNKNOWN` | The bridge does not know the handle, or the agent has no session with that name. |
| `SESSION_BUSY` | A different lease holds the named session. |
| `SESSION_REVOKED` | The owner revoked the named session. |
| `BAD_LABEL` | The label is not correct, or it is a label of the pool. |
| `NO_FREE_SESSION` | No pool session of the agent is free. Call `login` with a named session, or try the call again after some time. |
| `BRIDGE_ONLY` | A call of `login` or `logout` came to the server, not to the bridge. |
| `NOT_LOGGED_IN` | The call is not possible before a login. |
| `FORBIDDEN_ROLE` | The roles of the session do not let the session use the tool. |
| `RECIPIENT_UNKNOWN` | The address is not known. |
| `UNKNOWN_MESSAGE` | The `ref` is not known. |
| `BODY_TOO_LARGE` | The body is too large. |
| `REFUSED_HEADER` | A header is not permitted. |
| `RETRY_KEY_REUSED` | The retry key was used with different arguments. |
| `THREAD_CLAIMED` | A different session has the claim of the thread. |
| `MAIL_SERVER_DOWN` | Dovecot or Postfix does not operate. |
| `STORE_UNAVAILABLE` | The database does not operate. |
| `SERVER_UNAVAILABLE` | Only the bridge gives this code. The lease request of the bridge got a network error, or an error with no code. Try the call again after some time. |
| `BUSY` | Too many waits are open, or a lease operation waited too long for a different lease operation. |
| `INTERNAL` | An internal error occurred. |
| `INVALID_TRANSITION` | The item cannot go to that state. |
| `WIP_LIMIT` | The number of items in progress is at the limit. |
| `SUPERVISOR_TAKEN` | A different session is the supervisor. |
| `EVIDENCE_INCOMPLETE` | The evidence of the item is not complete. |
| `DEPENDENCY_OPEN` | A dependency of the item is not done. |
| `UNKNOWN_ITEM` | The item is not known. |
| `UNKNOWN_PROJECT` | The project is not known. |

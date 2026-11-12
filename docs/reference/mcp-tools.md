# Reference: MCP tools

The server name is `mai`. The contract version is `2.1.0`.

Before a login, `tools/list` gives only `login` and `whoami`. On stdio, the
server sends `notifications/tools/list_changed` after a login. On HTTP,
`tools/list` gives all 22 tools.

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
| `session` | A session handle: `ses-` and 26 base32 characters | `ses-...` |
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
| `SESSION_UNKNOWN` | The session handle is not known. |
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
| `BUSY` | Too many waits are open. |
| `INTERNAL` | An internal error occurred. |
| `INVALID_TRANSITION` | The item cannot go to that state. |
| `WIP_LIMIT` | The number of items in progress is at the limit. |
| `SUPERVISOR_TAKEN` | A different session is the supervisor. |
| `EVIDENCE_INCOMPLETE` | The evidence of the item is not complete. |
| `DEPENDENCY_OPEN` | A dependency of the item is not done. |
| `UNKNOWN_ITEM` | The item is not known. |
| `UNKNOWN_PROJECT` | The project is not known. |

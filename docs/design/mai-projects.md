# mai projects: sessions that do the work of one project

Status: design, revised on 14 November 2026 with two prototypes and one
review (appendix A). No part is in a release. Base: mai 3.3.1 (`b1880ac`).

A mark [A] shows an assumption that has no test. A mark [E] shows an
estimate. G-n is a gap of mai 3.3.1 (section 2). R-n is a requirement from
a real project, named shop-api in this document. D-n is a decision for the user (section
14). Each path is relative to the root of the repository.

## 1. Result first

- **A project is a board project with a link to a git repository.** The
  plan is `.mai/project.json`. `mai project apply` makes the server agree
  with it.
- **A member is one named session with one role in one project:**
  supervisor, worker, reviewer, or observer. Roles are not properties of
  agents.
- **Each member has a stable address,** for example `shop-api.lane-a`, and
  `shop-api.supervisor` goes to the supervisor.
- **A chat finds its member from its folder.** A Claude Code bridge binds to
  the member of its folder. In the other apps, the hook line or `AGENTS.md`
  gives the address.
- **Projects are separate for each session, not for each chat.** A member
  has no agent queue, and sends mail only in its project and to the owner.
  Section 8 gives the guard of each app.
- **Board notices go to member sessions,** never to agent addresses. With no
  supervisor chat, the owner gets one signal.
- **Only the owner approves.** The owner commands refuse to run in an agent
  app or with no terminal.
- **Releases:** 3.4.0 (model, isolation, folder pin), 3.5.0 (binding, hook
  lines, presence), 3.6.0 (Cursor guard, standby). Total: 12 to 16 days [E].

## 2. The problem

In shop-api, one Claude Code session was the supervisor. Cursor and
Antigravity were implementers, each in its own git worktree. mai 3.3.1
cannot give this setup safely:

| Gap | Fact in 3.3.1 | Evidence |
|---|---|---|
| G-1 | Only `mai exec -- project seed` makes a project, with one worker. | `src/mai/app_cli.py:225-264` |
| G-2 | A role is a property of an agent. Two Claude Code chats cannot be supervisor and worker. | `postgres_credential_store.py:31-36` |
| G-3 | The login reads the roles one time. | `log_in_command.py:55` |
| G-4 | A lane holds an agent. `start_item` takes the first item of the agent. | `postgres_board_store.py:559-564` |
| G-5 | Board notices go to the agent address, which each chat of the app reads. | `postgres_board_store.py:241-246` |
| G-6 | One agent queue holds the mail of all projects. | `mail_rules.py:115-119` |
| G-7 | In each chat, the hook shows the lines of all sessions of the bridge. | `cli/internal/hook/notice.go:57-69`, `format.go:94-104` |
| G-8 | The board tool schemas do not agree with the board. | `tool_schemas.py:444-575` |
| G-9 | 30 minutes after the last supervisor call, supervisor mail goes to the agent queue. | `postgres_board_store.py:226-239` |
| G-10 | No code reads `board.project.repo_path`. | `board_migrations.py:15` |

Outside evidence:

- Claude Code closed the request for MCP resource subscriptions (issue
  7252) as not planned. Thus notices go through hooks and `wait_for_mail`.
- MCP Agent Mail keys a project on the path of its repository.
- Cursor got more good results from planners, workers, and a judge, and
  Cognition from a reviewer with a clean context. GitHub does not let an
  author approve its change. Thus mai has three working roles, and a holder
  cannot accept its item.

| R | Section | R | Section |
|---|---|---|---|
| R-1 project from the host CLI | 9.1 | R-9 board with no supervisor | 7.4 |
| R-2 role of a session | 3.1 | R-10 only the owner approves | 8.2 |
| R-3 lane of a session | 6 | R-11 projects stay separate | 5.2 |
| R-4 notices to sessions | 6 | R-12 rules of a real project | D-14, in part |
| R-5 one command | 9.1 | R-13 folder and app of a lane | 4.2, 7.3 |
| R-6 a call with no session | 5.3 | R-14 pause | 7.5 |
| R-7 scope of a notice | 4.3 | R-15 guide | 9.3 |
| R-8 presence | 7.3 | R-16 exclusive session | D-07 |

## 3. The model

### 3.1 Projects, members, and roles

The login of a member session is `AGENT.PROJECT-MEMBER`, for example
`cursor.shop-api-lane-a`. Thus one app can be a member of many projects, and
two projects never use the same label. Nobody types the login. Chats and
senders use the member address `PROJECT.MEMBER`.

| Role | Board tools | Number |
|---|---|---|
| supervisor | All | Exactly one |
| worker | `get_board`, `read_item`, `start_item`, `report_status`, `submit_for_review` | One for each lane |
| reviewer | `get_board`, `read_item`, `report_status`, `review_item` | Zero or more |
| observer | `get_board`, `read_item` | Zero or more |

A role is a property of a session (D-01), because in shop-api the
supervisor and 8 implementers were Claude Code sessions (G-2). The owner is
not a member, and can call each tool, as in 3.3.1. A session is a member of
one project at most (D-02). Thus its WIP count and its mail scope are those
of one project. `board_rules.ROLES` and `TOOL_ROLES` (`board_rules.py:39`,
`:58-68`) and `WORK_ROLES` (`tool_schemas.py:11`) add `observer`.

### 3.2 The project file

`mai project init` writes the file, `mai project add` changes it, and the
user commits it:

```json
{
  "mai_project": 1,
  "name": "shop-api",
  "goal": "The API of the shop",
  "wip_limit": 2,
  "done_rule": {"checks": [{"name": "check", "required": true}]},
  "members": {
    "supervisor": {"agent": "claude", "role": "supervisor", "folder": "."},
    "lane-a": {"agent": "cursor", "role": "worker", "folder": "../shop-api-cursor"},
    "lane-b": {"agent": "antigravity", "role": "worker", "folder": "../shop-api-antigravity"},
    "review": {"agent": "codex", "role": "reviewer", "folder": "."}
  },
  "base": "sha256:..."
}
```

`project.Load` in the CLI examines these rules, and the server examines
them again at apply:

1. `folder` is relative and not empty (`.` is the root). The file has no
   absolute path, no key, and no instance name.
2. `mai_project` is 1. The JSON has no unknown field, no duplicate key, and
   no second value. `encoding/json` keeps the last of two equal keys, thus a
   hand edit could lose a member.
3. A project name agrees with the label rule, a member name with
   `[a-z0-9][a-z0-9-]{0,23}`, and an agent name with
   `[a-z0-9][a-z0-9-]{0,47}`. `PROJECT-MEMBER` has 32 characters or
   less, or the member gives `label`. A label never has the pool form
   `s[0-9]+`, and two members never get one login.
4. A project name is never an agent name. The CLI knows only the agents of
   the file. Thus `apply` refuses a project name that is an agent, and
   `AddAgentHandler` (`add_agent_command.py:32-40`) refuses an agent name
   that is a project.
5. The names `supervisor` and `reviewer` are role addresses, for that role
   only. Exactly one member is supervisor.
6. One folder has one member of an agent at most.

The format is JSON, because the Go CLI has no TOML parser (`cli/go.mod`).

**Worktrees.** Git checks out a copy of a committed file in each linked
worktree. The CLI prototype showed that a search that stops at the first
file resolves `.` to the worktree (appendix A.1). Thus, when the found
folder has a `.git` file, `project.Find` reads `gitdir` and `commondir` with
no git command. If the primary worktree has the file, `Find` gives it.

**Copies of an earlier plan.** Each branch has its own copy of the plan.
Thus `apply` writes the SHA-256 of the applied plan in `base`, and the SHA
does not include `base`. The server refuses a removal of a member when
`base` is not its `plan_sha` (`409 PLAN_STALE`).

### 3.3 The tables

Migration 101 (credentials) moves the roles to sessions. The role table stays
`mail_auth.membership`, thus mai keeps one role system:

```sql
ALTER TABLE mail_auth.membership RENAME COLUMN agent TO login;
ALTER TABLE mail_auth.membership ADD COLUMN member text,
  ADD COLUMN folder text, ADD COLUMN removed_at timestamptz;
UPDATE mail_auth.membership m SET removed_at = now()
  FROM mail_auth.principal p WHERE p.login = m.login AND p.kind = 'agent';
ALTER TABLE mail_auth.membership ADD CONSTRAINT membership_role_check
  CHECK (role IN ('supervisor','worker','reviewer','observer')) NOT VALID;
CREATE UNIQUE INDEX membership_one_project ON mail_auth.membership (login)
  WHERE removed_at IS NULL;
CREATE UNIQUE INDEX membership_member ON mail_auth.membership (project, member)
  WHERE removed_at IS NULL;
CREATE UNIQUE INDEX membership_supervisor ON mail_auth.membership (project)
  WHERE role = 'supervisor' AND removed_at IS NULL;
CREATE UNIQUE INDEX membership_folder ON mail_auth.membership
  (folder, split_part(login, '.', 1)) WHERE removed_at IS NULL;
```

Migration 510 (board) links the project to its repository and the lane to
its session:

```sql
ALTER TABLE board.project ADD COLUMN remote text, ADD COLUMN plan_sha text,
  ADD COLUMN state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','paused')),
  ADD COLUMN state_note text, ADD COLUMN offline_signal_at timestamptz;
ALTER TABLE board.lane ALTER COLUMN agent DROP NOT NULL, ADD COLUMN branch_prefix text,
  ADD COLUMN login text REFERENCES mail_auth.principal(login);
CREATE UNIQUE INDEX lane_one_login ON board.lane (project, login) WHERE login IS NOT NULL;
CREATE TABLE board.presence (project text NOT NULL REFERENCES board.project(name),
  login text NOT NULL, last_call_at timestamptz NOT NULL, PRIMARY KEY (project, login));
ALTER TABLE board.approval ADD COLUMN action text, ADD COLUMN process jsonb;
```

The app role has no DELETE right, and the primary key `(project, login)`
stays. Thus `apply` adds a removed member again with an UPDATE that sets
`removed_at` to NULL, and its revoked session goes back to `issued`. The
UPDATE of agent rows runs before the constraint, thus an earlier role
`owner` cannot stop the migration.

## 4. How a chat finds its member

### 4.1 One form for each folder

`project.Resolve` in the CLI resolves symbolic links. For a folder that does
not exist, it resolves the nearest parent that exists and adds the rest. On
macOS and Windows, it also gives the true case of the letters (`F_GETPATH`,
`GetFinalPathNameByHandle`) [A]. `apply`, the bridge, and the hook use
`Resolve`. At this time, the bridge sends `os.Getwd()` with no change
(`cmd_runtime.go:49`). The lease request adds the host OS, and for `darwin`
and `windows` the server compares folders with no case, as `foldKey` does.

The CLI prototype showed why each part is necessary. On macOS, `filepath.Clean`
keeps `/var/...`, but a chat after `git worktree add` gives
`/private/var/...`. `EvalSymlinks` keeps the case of its input on APFS.
Cost: on a case-sensitive volume, two folders with different case agree.

The match (`MemberOf` in the CLI, `of_folder` on the server) uses the
longest member folder on a path boundary. Thus `shop-api/src` gives the
member of `shop-api`, but `shop-api-cursor` does not. A worker worktree in
the root (`.claude/worktrees/lane-c`) gives that worker. With no agent, two
members at the longest folder give no match.

### 4.2 Claude Code: the folder binding

Claude Code starts one bridge for each chat process, in the folder of the
chat. Thus `PER_CHAT_APPS = ("claude",)` (D-06):

```text
claude (cwd /Users/me/work/shop-api)
  -> mai mcp --agent claude        folder = Resolve(Getwd)
  -> POST /api/leases {app: "claude", os: "darwin", folder, pid, bind: true}
server: of_folder("claude", folder) = claude.shop-api-supervisor
  -> lease on that session, result {session, bound: "folder", member: {...}}
bridge: no session, the login, or the member address: the bound lease
```

1. The server binds only on `bind: true`, which the bridge sends only for
   its first lease. If a different bridge holds the member, the result is a
   pool session with `member_busy`.
2. Leases are in memory (`sessions_module.py:39`), and end 90 s after the
   last heartbeat. After a restart or a sleep, a bound bridge gets a lease
   again with its member login (`lease.go:405`), and a pool bridge with
   `bind: false`. Thus a chat that started after it cannot take the member.
3. If the member is busy at that time, each call of the bound bridge gives
   `SESSION_BUSY` (path `session`). The bridge does not go to the pool.
4. `leaseOf` (`session.go:482`) maps the login and the address of the bound
   member to the bound lease. If not, an explicit `session` gets
   `SESSION_BUSY` (`lease_service.py:503`).

Codex goes into `PER_CHAT_APPS` only after a test shows that two Codex threads
start two bridges, in the app and in the IDE extension.

The code changes:

- `LeaseService._acquire`: on `bind: true` from an app in `PER_CHAT_APPS`,
  call `CredentialService.member_of_folder(agent, folder, os)`.
- `runMCP` sends the known app name. `guideAppOfProc` (`cmd_guide.go:503`)
  moves to `procchain.App` and adds `agy`, which gives `copilot` in a Code
  terminal at this time (R-13).
- `bridge.go` keeps `bound` and `member` for `whoami` and the notice file.

### 4.3 Shared bridges: the hook line

One Cursor bridge served all windows and held five leases.
Thus the folder of the bridge is not the folder of the chat, and the hook
tells each chat its member:

```text
mai: this chat is shop-api.lane-a (worker in project shop-api).
Give session: "shop-api.lane-a" in each mai tool call. 2 new mail.
Read only the mail of shop-api.lane-a.
```

In Claude Code, the second sentence is "Your folder gives your session. Give
no session." The method:

1. Each heartbeat gives all members of the agent and their new-mail counts
   (`ImapNewMailCounter`), with or without a lease in this bridge. Thus the list is correct after an apply, and a notice shows
   before the first call of the chat.
2. In Claude Code, the hook uses the bound member, because the payload
   `cwd` can change after a `cd` in the Bash tool [A].
3. In the other apps, the hook reads `cwd` or `workspace_roots[0]` (table).
   A `file://` URI becomes a path. With no folder, the hook uses the folder
   where it runs.
4. `hook.Lines` gives only the line of that member. At the start and at
   each prompt, it gives the line also with no new mail. At a stop and at
   `PostToolUse`, it gives a line only for new mail.
5. With no member for the folder, the output stays as in 3.3.1.

| App | Folder source | Limit |
|---|---|---|
| Claude Code | Bound member | None |
| Cursor | `workspace_roots[0]` [A] | A window with many roots uses the first root. |
| Codex, Copilot | `cwd` [A] | Copilot has no prompt hook. 3.5.0 adds `userPromptSubmit` [A]. |
| Antigravity | None known | Many hub processes, and `PreInvocation` before each model call. The 3.3.1 lines stay. |

Until a recorded payload shows a folder field, an Antigravity member gets
its address only from `AGENTS.md` [A]. `mai project init --agents-md` adds a
marked block with the folders and the members to that file.

### 4.4 The session argument

A chat can give its session as the member address `shop-api.lane-a`, the
login `cursor.shop-api-lane-a`, or the label `shop-api-lane-a`. A name with
a dot whose first part is not the agent of the bridge is a member address.
The server resolves it to the login of a member of that agent, else gives
`SESSION_UNKNOWN`. A member name alone (`lane-a`) is a label, as in 3.3.1,
because two projects can each have a `lane-a`. The changes are in
`lease_service.py:_named_session`, `session.go:leaseOfLogin`, and `openOnce`
(`session.go:334`), which must accept the login that the server gives for an
address.

## 5. Addresses and isolation

### 5.1 Member and role addresses

Each member and role address is a row of `mail_auth.alias`, which Postfix
and the mail UI read (`etc/postfix/pgsql-alias.cf`). `apply` writes the rows in the transaction of
the members:

| Address | Goes to |
|---|---|
| `shop-api.supervisor` | The supervisor member |
| `shop-api.reviewer` | The reviewer if the project has exactly one, else the supervisor |
| `shop-api.MEMBER` | The session of that member |

`apply` tells when `shop-api.reviewer` goes to the supervisor. The address
of a removed member goes to `owner`.

The sender of a member mail is its login, because Postfix
(`reject_sender_login_mismatch`) and `sender_proved` (`message.py:224`)
accept only the SASL login as From. Thus each mail of a member gets
`Reply-To: PROJECT.MEMBER`, and views show the member address next to the
login. `parse_summary` (`message.py:241`) trusts `X-Mai-Project` from a
proved member sender, not only from `board`.

### 5.2 The rules of isolation

Isolation is for each session: a member session has no path to the mail of a
different project. A chat in a shared bridge can give the address of each
member of its agent, because `_named_session` examines only the agent
(`lease_service.py:473-504`). Section 8 gives the guard of each app, and D-13
gives isolation for each key.

`SessionState` carries the member flag from the membership row. With that
flag, these parts use no agent queue:

| Part | Change |
|---|---|
| `mail_rules.queue_of`, `mail_reader.queue_of` | No queue for a member. The mail tools read only its INBOX. |
| `notice_lines.mailboxes_of` | No queue in the heartbeat count and the notice watcher. |
| `NewSessionHandler` (`new_session_command.py:77`) | No grant for a member session. Thus no removal is necessary. |
| `GrantMailboxAccessHandler` (`grant_mailbox_access_command.py:49-55`) | No grant for a member at a start of Dovecot. If not, each restart gives the rights again. |
| `RevokeSessionHandler` (`revoke_session_command.py:32-42`) | Unread mail of a member goes to `owner`, not to the agent queue. |
| `ClaimService`, `SessionStaleJob` | No claim and no return for a member (section 6). |
| `MailSender.check_known` | On logins, after alias resolution. A member sends only in its project and to `owner`. Each other recipient gives `RECIPIENT_OUT_OF_PROJECT`. |
| `list_addresses_query.py:16-35` | A member sees its project and `owner`. A session that is not a member sees no member. |
| `mail_sender.build`, `Call.resolve_project` | `X-Mai-Project` on each member mail. A board call acts only in the project of the session. |

When a session that exists becomes a member, `apply` removes its queue ACL
before it writes the member row. If the removal does not occur, `apply`
stops. Mail between projects is not possible (D-05). The owner copy
(`copies`) continues to get each mail.

### 5.3 A call with no session (R-6)

In the 3.1.0 smoke test, five Cursor chats gave no session and read the pool
INBOX. Two changes:

1. A call with no session gets `SESSION_REQUIRED` (path `session`) when its
   `ref` is in the mailbox of a named session of the same agent.
2. `mai agent pool off AGENT` closes the pool (D-15). Then each call with no
   session from a bridge that is not bound gets `SESSION_REQUIRED`.

## 6. The task flow

The states of the board stay the same (`board_rules.py:43-53`):

| Step | Who | Tool | State | Notice to |
|---|---|---|---|---|
| 1 | Supervisor | `plan_item` (key, title, spec, lane, reviewer) | `backlog` to `ready` | None |
| 2 | Supervisor | `assign_item` (item, lane) | `ready` to `assigned` | Lane session |
| 3 | Worker | `start_item` (no item) | `assigned` to `in_progress` | Supervisor |
| 4 | Worker | `report_status` (text, state, cause) | `in_progress` or `blocked` | Supervisor |
| 5 | Worker | `submit_for_review` (evidence) | `in_progress` to `in_review` | Reviewer, supervisor |
| 6 | Reviewer | `review_item` (changes, findings) | `in_review` to `changes_requested` | Holder |
| 7 | Reviewer | `review_item` (accept, no merge commit) | Stays `in_review`, signal `review_accepted` | Supervisor |
| 8 | Supervisor | `review_item` (accept, merge commit) | `in_review` to `done` | Holder, reviewer |

The code changes, in `postgres_board_store.py` if no file is given:

- `Call.resolve_project` and `whoami` read the role of `st.login` in the
  call (G-3). The dispatcher removes its role filter of the login time, for
  the board tools (`dispatcher.py:260-261`) and in `tools/list` (`:205`).
- `_assign_item` sets `assignee` to the login of the lane (`lane_login`).
- `_start_item` with no item takes the first item of the lane of the caller
  (R-3), and an item of a different lane gives `FORBIDDEN_ROLE`. The WIP
  count adds `AND project = %s`.
- `_start_item` does not call `claim_thread` (`:571`) for a member, because
  `ClaimService` reads the agent queue (`claim_service.py:65-72`). The lock
  is `item.holder` with `FOR UPDATE SKIP LOCKED` (D-11). `ClaimService` and
  `SessionStaleJob` (`session_stale_job.py:84-91`) skip member logins.
- `item_targets` is the holder, then the lane session, then the supervisor.
  `supervisor_target` is the supervisor member, else `owner` (R-4).
- `take_supervisor` records presence only, and `SUPERVISOR_TAKEN` goes.
- The `reviewer` of `plan_item` is a member name, and the board stores its
  login. `_review_item` compares it with `st.login`, not `st.agent`
  (`:918`). `_get_board` shows `lane.login` (`:427-441`).
- `_review_item`: a caller whose login is the holder or an evidence actor of
  the item gets `OWN_ITEM`, the supervisor included. Only the owner can skip
  this rule. For an item that the supervisor holds, a reviewer accept with
  the merge commit sets `done` (D-09).
- `_submit_for_review` sends the request to the item reviewer, else to
  `shop-api.reviewer`, with a copy to the supervisor.

A board mail gets `Reply-To`: the supervisor for a worker or reviewer, and
the lane for the supervisor.

## 7. Notices, standby, and an offline supervisor

### 7.1 Notice lines

The heartbeat result (`lease_service.py:219-240`) adds up to three entries
`{kind, from, item}` of new mail. The server makes each field from the kind
header, the proved sender, and `X-Mai-Item`. An entry has no subject,
because an agent writes the subject. A hook line then tells, for example,
`1 request from shop-api.supervisor, item itm-abcdefghijkl`.

### 7.2 The chat that waits

1. **All apps:** the chat calls `wait_for_mail` with `timeout_sec` 50, a
   maximum of 30 times (25 minutes). Then it stops and tells the user. Each
   loop costs one model turn [E].
2. **Claude Code, 3.6.0:** a `Stop` hook with `asyncRewake` [A] runs
   `mai hook --wait`, which reads the notice file each 2 seconds. On new
   mail, it writes the line and exits with code 2, and Claude Code starts
   the chat again. The wait costs no tokens.
3. **Cursor:** the `stop` hook gives `followup_message` (`loop_limit` 1). A
   stop hook that holds the chat is not safe [A], thus Cursor uses method 1.

### 7.3 Presence on the board

`get_board` gives, for each member (R-8, R-13):

```json
{"member": "lane-a", "role": "worker", "folder": "/Users/me/work/shop-api-cursor",
 "lease": {"app": "cursor", "pid": 3301, "bridge_folder": "/Users/me/work"},
 "last_call_at": "2026-11-14T10:02:11Z", "waiting": true, "current_item": "itm-abcdefghijkl"}
```

`folder` comes from the membership row. `bridge_folder` is the same for each
member of one Cursor bridge. The dispatcher records each call of a member in
`board.presence`, with one write each 60 s at most. Thus `last_call_at`
stays after a restart. `waiting` is true during an open `wait_for_mail`.

### 7.4 An offline supervisor

The supervisor is a stable session, thus its mail stays in its INBOX while
no chat runs. The next chat in its folder gets the same member and reads
that mail. Workers continue, and their items stay in `in_review`.

A new job `board/jobs/supervisor_offline_job.py` runs each 60 seconds. It
reads `last_call_at` of the supervisor in `board.presence`, and the age of
the items in `in_review` or `blocked`. The owner gets one `signal` mail when
the two are 30 minutes or more. The job then sets `offline_signal_at`, and it
sends again only after a call that is after that time. Each tool call
counts, thus a supervisor that only reads is present. There is no second
supervisor (D-08).

### 7.5 Pause

`mai project pause shop-api --note TEXT` sets `state` to `paused`. Then
`assign_item` and `start_item` give `PROJECT_PAUSED`, and each member gets
one notice with the note (R-14). Reports and reviews continue.
`mai project resume` sets `active`.

## 8. A chat that gives the lane of a different chat

### 8.1 Proofs and guards

| mai proves | mai cannot prove |
|---|---|
| The agent, through the agent key | Which chat in one bridge made a call |
| The bridge, through the lease token in its memory | The app and the folder of the bridge. The bridge reports them (`leases_controller.py:104-119`). |
| One lease for each member at a time (`SESSION_BUSY`) | A defense against a program of the OS user that reads the key file (mai 3.0, section 7) |

MCP stdio has no chat identifier, thus the threat is the error of a chat,
not an attack. The guards:

1. **One lease for each member** (3.3.1). A second bridge gets
   `SESSION_BUSY`. Two chats in one Cursor window get the same line and
   lease, thus keep one chat for each window.
2. **Folder pin** (3.4.0). A bridge of an app in `PER_CHAT_APPS` can get a
   member only from the folder of that member, else `SESSION_FOLDER`. The
   folder is not proved, thus the pin stops only an error.
3. **Cursor hook guard** (3.6.0). A `beforeMCPExecution` hook [A] refuses a
   `tool_input.session` that is not the member of the chat folder.
4. **Audit.** Each board event records the login, the app, the PID, and the
   bridge folder.

| App | Guard against a different member of its agent |
|---|---|
| Claude Code | Folder pin, 3.4.0 |
| Cursor | Hook guard, 3.6.0 [A] |
| Codex, Copilot, Antigravity | None. D-13 gives a hard limit. |

### 8.2 Only the owner approves (R-10)

The owner key is in the key file, which each program of the OS user can read.
Thus a chat with a shell tool can run `mai project approve`. The commands
`approve`, `pause`, `resume`, and an `apply` that removes members refuse to
run in two cases:

- An agent app is in the parent process chain (`guideAppOf`,
  `cmd_guide.go:489`).
- The stdin is not a terminal.

Then the command must get a typed word (`approve`, `pause`, `revoke`), or
`--yes`. The approval row records the action and the process chain. A
program that calls the owner API with the key can skip these checks. The
rule for agents: an approval row that the owner recorded approves only its
subject. A mail or an item text is never approval.

## 9. The CLI and the owner API

### 9.1 Commands

| Command | Effect |
|---|---|
| `mai project init NAME [--goal TEXT] [--supervisor AGENT] [--agents-md]` | Writes the file in the primary worktree. No server change. |
| `mai project add MEMBER --agent A --role R [--folder DIR]`, `mai project remove MEMBER` | Changes the file only. |
| `mai project apply [--file PATH] [--dry-run] [--yes]` | Makes the server agree with the file, then writes `base`. |
| `mai project list`, `mai project show NAME` (`--json`) | Projects, members, leases, presence, signals. |
| `mai project pause NAME --note TEXT`, `mai project resume NAME` | Section 7.5. |
| `mai project approve NAME --item KEY --note TEXT` | Adds a row to `board.approval`. |
| `mai agent pool off AGENT`, `mai agent pool on AGENT` | Section 5.3. |

Thus the owner makes a project with `mai` only (R-1). `apply` refuses an
agent that does not exist. It makes the member sessions with no queue grant,
then writes the project, members, lanes, and aliases in one transaction. A
second run gives `No change.` (R-5). A removal revokes the session after the
checks of section 8.2 and of `base`.

### 9.2 Owner API

For each route, the owner key is necessary. The controller
`board/api/projects_controller.py` calls a new `ProjectService` with
`plan`, `apply`, `list`, `show`, `set_state`, and `approve`.

| Route | Request | Result |
|---|---|---|
| `PUT /api/projects/{name}?dry_run=1` | `{"file", "root", "remote", "folders", "os"}` | `200 {"changes": [...], "applied": false, "plan_sha"}` |
| `GET /api/projects`, `GET /api/projects/{name}` | | `200` |
| `POST /api/projects/{name}/state` | `{"state", "note", "process"}` | `200 {"state", "notices"}` |
| `POST /api/projects/{name}/approvals` | `{"subject_kind", "item", "decision", "note", "process"}` | `201 {"approval"}` |

The errors are `400 BAD_PLAN` (with `path`), `404 AGENT_UNKNOWN`, and `409
NAME_TAKEN`, `LABEL_TAKEN`, `FOLDER_TAKEN`, or `PLAN_STALE`. The CLI sends
absolute folders, because the server in the container cannot read the host
folders. After migration 101, `project seed` gives an error, because
`app_cli.py:256` writes the column `agent` (D-10).

### 9.3 `mai guide --project`

The command finds the project file from the current folder, and changes
nothing (R-15). It gives the state and the steps that are not done:

```text
Project shop-api: file found, not applied.
Steps:
  1. mai agent add codex          (the agent codex does not exist)
  2. mai connect codex
  3. mai project apply
  4. mai agent pool off cursor    (all cursor chats are members)
  5. Open a chat in /Users/me/work/shop-api-cursor (member lane-a, Cursor).
This folder: member supervisor (claude).
```

The code is a new `guideRun.project` step in `cmd_guide.go`, next to
`lanes()` (`:965`).

## 10. The MCP contract

Contract 3.4.0 comes with mai 3.4.0 (`json_rpc.py:16`). The tool count stays
22.

| Tool | Change |
|---|---|
| `whoami` | Adds `project` (name, member, role, lane, address, supervisor, folder, state), read in the call. 3.5.0 adds `bound` and `member_busy`. |
| `list_addresses` | Adds `project` with its members and role addresses. |
| `send_message`, `reply` | Accept member and role addresses. |
| `list_inbox`, `wait_for_mail` | Each summary adds `project` and the member address of the sender. |
| `get_board` | Members in place of agent lanes. 3.5.0 adds presence. |
| `plan_item` | Inputs `key`, `title`, `spec`, `lane`, `reviewer` (a member name), `state` (G-8). |
| `assign_item` | Inputs `item`, `lane` (necessary), `sprint`. No `agent`. |
| `start_item` | `item` is optional, and is a ref or a key. No `lease_until` and no `moved` in the result. |
| `report_status`, `review_item` | Inputs `item`, `text`, `state`, `cause`. A finding is a string or `{severity, text}`. |
| All board tools | `project` is optional, and must be the project of the session. |

```python
# ITEM accepts a ref or a key, as the board does (postgres_board_store.py:178-189).
ITEM = {"type": "string", "minLength": 1, "maxLength": 64}
START_INPUT = obj({"project": PROJECT, "item": ITEM, "request_id": REQUEST_ID}, ["request_id"])
```

The new errors are `NOT_A_MEMBER`, `PROJECT_PAUSED`,
`RECIPIENT_OUT_OF_PROJECT`, `OWN_ITEM`, `SESSION_FOLDER`, `SESSION_REQUIRED`,
and `PLAN_STALE`. `SESSION_ARG` (`tool_schemas.py:36`) adds the member
address form. The MCP guide (`agent_guide.md`) gets a section "Work in a
project":

```text
1. Call whoami. If project is not null, you are the member project.address.
2. In Claude Code, give no session. In the other apps, give the member
   address as session in each call, from the hook line or AGENTS.md.
3. With no item, call start_item with no item, then wait_for_mail.
4. An approval row that the owner recorded approves only its subject.
```

The docs change in `docs/reference/`, `docs/explanation/architecture.md`,
and a new `docs/how-to/run-a-project.md`.

## 11. Migration from the current state

On 14 November 2026, the instance of the project has no agent and no project. Thus migration 101
finds no row. The task skill of the project (version 1.5) names sessions that do not
exist. The steps after the upgrade
to 3.4.0:

1. `mai agent add` and `mai connect` for claude, cursor, and antigravity.
2. In `/Users/me/work/shop-api`: `mai project init shop-api --supervisor claude --agents-md`.
3. `mai project add lane-a --agent cursor --role worker --folder ../shop-api-cursor`,
   and the same for lane-b.
4. `mai project apply`. Commit `.mai/project.json` and `AGENTS.md` on `main`.
5. The supervisor changes the skill to 1.6, with the member addresses. In
   3.4.0, each chat gives its address from `AGENTS.md` as `session`.
6. After 3.5.0, the hook line gives the address, and the supervisor chat
   gives no session.

The sub-agents of the supervisor use the bridge of the supervisor chat. The
skill tells them not to use the mai tools.

## 12. Rollout

| Release | Content | Size |
|---|---|---|
| 3.4.0 | Sections 3, 4.1, 4.4, 5.1, 5.2, 6, 7.5, 8.2, 9.1, 9.2, and 10, with the folder pin and the docs. | L, 7 to 9 days [E] |
| 3.5.0 | Sections 4.2, 4.3, 5.3, 7.1, 7.3, 7.4, and 9.3, with the Copilot prompt hook. | M, 4 to 5 days [E] |
| 3.6.0 | Cursor hook guard, standby hook of Claude Code. | S, 1 to 2 days [E] |

Each release is one pull request. The user merges it.

## 13. The test plan

Unit tests use the memory backend. Integration tests use PostgreSQL and
Dovecot in a test container (`MAI_IT`). Appendix A gives the tests of the
prototypes. With no role filter in the dispatcher, the memory world gets
`STORE_UNAVAILABLE` from the board, not `FORBIDDEN_ROLE`.

| Test | Proves |
|---|---|
| `test_a_start_item_with_no_item_gets_through_the_schema` (dispatcher) | Section 10 |
| `test_start_item_of_a_member_with_no_queue_right` (real Dovecot) | Section 6 |
| `test_a_second_apply_of_the_same_file_makes_no_change` | R-5 |
| `test_apply_adds_a_removed_member_again` | Section 3.3 |
| `test_apply_refuses_a_removal_from_a_copy_of_an_earlier_plan` | Section 3.2 |
| `test_a_ref_of_a_named_session_with_no_session_gives_session_required` | R-6 |
| `test_a_member_gets_no_queue_right_after_a_start_of_dovecot` | R-11 |
| `test_a_member_cannot_send_to_a_different_project` | R-11 |
| `test_a_reviewer_session_can_review_its_item` | Section 6 |
| `test_the_supervisor_cannot_accept_an_item_that_it_started` | Section 6 |
| `test_a_project_name_is_not_an_agent_name_in_each_order` | Section 3.2 |
| `test_start_item_gives_project_paused` | R-14 |
| `TestBridgeMapsTheMemberAddressToTheBoundLease` | Section 4.2 |
| `TestABoundBridgeKeepsItsMemberAfterARestartOfTheLeaseStore` | Section 4.2 |
| `TestProcChainGivesAntigravityForAgyInCode` | R-13 |
| `TestApproveRefusesToRunInAnAgentApp` | R-10 |
| `TestGuideProjectChangesNothing` | R-15 |

An end-to-end check on a scratch instance (ports 18880 to 18889, names that
start with `mai-wf-proj`) uses a supervisor chat in the root and two worker
chats. It records the hook lines and the notice targets of one item, with
one restart of the container.

## 14. Decisions for the user

| ID | Decision | Recommendation |
|---|---|---|
| D-01 | Roles are properties of sessions. Agent roles go. | Yes. Two Claude Code roles are necessary. |
| D-02 | A session is a member of one project at most. | Yes. The other method is a `project` argument in each mail tool. |
| D-03 | The plan is committed. The CLI reads the primary worktree, and `base` stops removals from an earlier copy. | Yes. With no file, the guide cannot find a project. |
| D-04 | Addresses `PROJECT.MEMBER` through `mail_auth.alias`. A project name is never an agent name. | Yes. Postfix and the mail UI use that table. |
| D-05 | No mail between projects. | Yes. |
| D-06 | Folder binding and the folder pin for Claude Code only. | Yes. Codex goes in after a test of its threads. |
| D-07 | No exclusive members (R-16) until they are necessary. | Yes. In 3.1.0, chats lost the handle. |
| D-08 | No second supervisor. The owner gets one signal. | Yes. One user keeps the scope and the push. |
| D-09 | A reviewer accept keeps the item in review. The supervisor sets done with the merge commit. | Yes. In shop-api, the supervisor merges. |
| D-10 | `project seed` goes in 3.4.0. | Yes. No project exists. If it stays, it must write `login` and `member`. |
| D-11 | Items use `item.holder` as the lock, not thread claims. | Yes. A member has no agent queue. |
| D-12 | Standby of Claude Code with `asyncRewake` in 3.6.0. | Yes, after a check of `asyncRewake`. |
| D-13 | A hard limit for a shared bridge: one agent for each app and project, for example `cursor-shop-api` [A]. | Document it, not as the default. It costs one key and one connect. |
| D-14 | R-12: finding severities in 3.4.0. Many done rule items, a WIP limit with `in_review`, gates, and review rounds in a different proposal. | Yes. They do not change the identity of sessions. |
| D-15 | `mai agent pool off` is an owner command, and `apply` does not set it. | Yes. Claude Code chats outside a project use the pool. |

## Appendix A. Prototype evidence

Each prototype starts at `b1880ac`, is not pushed, and has author and
committer Moses Gameli. No prototype installed a binary or touched the live
instance.

### A.1 Go CLI: the member of a chat folder

Branch `proto/projects-cli`, commit `b0abac4`. New
`cli/internal/project/project.go` and tests. Changed
`cli/internal/hook/format.go` and `notice.go`. Proved:

- `TestFindGoesUpToTheRootOfTheProject`, and `ErrNotFound` at the root of
  the file system.
- `TestFindInALinkedWorktreeGivesThePrimaryWorktree`, with a real
  `git worktree add`. The test also shows the bug of a search that stops at
  the worktree.
- `TestLoadResolvesARelativeFolderThroughASymbolicLink` and
  `TestLoadResolvesTheParentOfAMissingFolder`.
- `TestLoadRefusesTwoMembersOfOneAgentInOneFolder`,
  `TestLoadRefusesALabelOfMoreThan32Characters`, and
  `TestLoadRefusesEachBrokenRule` (18 cases).
- `TestMemberOfUsesALongestFolderWithAPathBoundary` and
  `TestMemberOfIgnoresTheCaseOfLettersOnMacOSAndWindows`.
- `TestHookShowsOnlyTheMemberOfTheChatFolder`,
  `TestHookReadsTheWorkspaceRootOfCursor`, and
  `TestTheJSONOfANoticeKeepsTheMembers`.
- `TestHookWithNoFolderKeepsEachLine`: for 10 format and event pairs, the
  output is the same as in 3.3.1. The hook tests of 3.3.1 pass with no
  change.

Design changes from it: `Find` and the file rules (3.2), `Resolve` and the
case rule (4.1), and the hook rules (4.3). A member with no lease showed no
count, thus the heartbeat gives all counts.

Commands, in `cli` of the worktree, with `HOME`, `USERPROFILE`, `AppData`,
`XDG_CONFIG_HOME`, `MAI_HOME`, and `GOCACHE` in a scratch folder. Set
`GOPROXY=off`, and `GOMODCACHE` from `go env` before the `HOME` change.

```sh
gofmt -l .; echo "exit=$?"
go test ./internal/project/ ./internal/hook/ -count=1 -v > test.log 2>&1; echo "exit=$?"; grep -c FAIL test.log
go vet ./...; echo "exit=$?"
go test ./... -count=1 > all.log 2>&1; echo "exit=$?"; grep -c FAIL all.log
```

Results: `gofmt` gave no file. The two packages gave 23 tests that pass (13
new), 0 `FAIL`, and 0 skipped. `go vet` gave exit 0, and all 17 packages
pass. The STE lint of the prose gave 0 errors.

### A.2 Python server: roles and lanes of sessions

Branch `proto/projects-server`, commit `7b23c5f`. It changes the two
migrations, `postgres_board_store.py`, the credential stores,
`log_in_command.py`, and `dispatcher.py`. Proved in a container with
PostgreSQL 18.6, in `tests/modules/board/test_session_roles_it.py`:

- `test_a_worker_session_of_the_supervisor_agent_cannot_plan_an_item`
- `test_start_item_with_no_item_takes_only_an_item_of_the_lane_of_the_caller`
- `test_the_assign_notice_goes_to_the_lane_session_only`
- `test_a_new_role_has_an_effect_on_the_next_call`
- `test_board_mail_goes_to_the_supervisor_member_with_no_supervisor_lease`
- `test_a_live_lease_of_a_different_session_does_not_stop_the_supervisor`
- `test_the_wip_count_ignores_the_items_of_a_different_project`. A copy with
  no project condition gives `WIP_LIMIT` there (mutation check).
- `test_migration_101_removes_the_roles_of_agents_and_keeps_earlier_role_names`,
  on rows of 3.3.1.

`test_postgres_round_trip` and `test_stdio_login_whoami_logout` of 3.3.1
also pass.

Limits: the fixture called `PostgresBoardStore.call` with a fake mailer and
a fake claims service. Thus it did not use the tool schema or the claim
through the agent queue, and the review found an error in each (sections 6
and 10). The commit ran with no lefthook hooks, and the checks ran by hand.

Design changes from it: the UPDATE that adds a member again and the lane
index (3.3), and `observer` (3.1). Also: no role filter in `tools/list`, the
login in `_review_item` and `_get_board` (6), and D-10.

Commands. `WT` is the worktree, and `PYLIB` is a copy of the pure Python
packages of pytest. `V` is the `bin` folder of the primary venv. `HL` holds
the locked wheels of `jsonschema` and its dependencies.

```sh
docker run --rm --pull=never --name mai-wf-proj-srv --entrypoint sh \
  -e PYTHONDONTWRITEBYTECODE=1 -e PYTHONPATH=/w/src:/pylib \
  -v "$WT":/w:ro -v "$PYLIB":/pylib:ro ghcr.io/monorithm/mai:3.3.1 -c '
  su postgres -c "initdb -D /tmp/pg && pg_ctl -D /tmp/pg -o \"-k /tmp -c listen_addresses=\" -w start" &&
  cd /w && export D="host=/tmp dbname=postgres user=postgres" &&
  MAI_PROTO_DSN="$D" /opt/mai/.venv/bin/python -m pytest -p no:cacheprovider -q tests/modules/board; echo "exit=$?";
  MAI_TEST_DSN="$D" /opt/mai/.venv/bin/python -m pytest -p no:cacheprovider -q \
    tests/modules/sessions/test_postgres_stores.py tests/modules/sessions/test_session_tools.py; echo "exit=$?"'
PYTHONPATH=src:$HL $V/python -m pytest -p no:cacheprovider -q; echo "exit=$?"
$V/ruff check --no-cache src tests; $V/ruff format --no-cache --check .; $V/ty check --python "$V/.." --extra-search-path $HL
```

Results: `tests/modules/board` gave 30 passed and 1 skipped (`MAI_IT`), with
the 8 new tests. The sessions tests gave 4 passed. The host suite gave 588
passed and 46 skipped. `ruff`, `ruff format`, and `ty` gave exit 0. No
container or volume with the name `mai-wf-proj` stayed.

With no `HL`, the host command gives exit 2 and 3 collection errors, also on
the base, because the primary venv has no `jsonschema` (`pyproject.toml:20`
has it). Thus the release check uses a scratch venv from
`uv sync --frozen`, with `UV_PROJECT_ENVIRONMENT` and `UV_CACHE_DIR` in a
scratch folder.

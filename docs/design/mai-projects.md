# mai projects: sessions that do the work of one project

Status: design, revised on 14 November 2026 for U-01 and a second review,
with two prototypes, one tools test, and review tests (appendix A).
No part is in a release. Base: mai 3.3.1 (`b1880ac`).

A mark [A] shows an assumption that has no test, and [E] shows an estimate.
G-n is a gap of mai 3.3.1 (section 2), and R-n is a requirement of a real
project, named shop-api here. U-n is a decision of the user, and D-n is a
decision for the user (section 14). Paths are relative to the root of the
repository.

## 1. Result first

- **A project is a board project with a link to a git repository.** The
  plan is `.mai/project.json`, and `mai project apply` makes the server
  agree with it.
- **A member is one named session with one role in one project:**
  supervisor, worker, reviewer, or observer.
- **Each worker has a lane, a git worktree at
  `<project-folder>/.mai/<agent>/<lane-id>`** (U-01). The project folder is
  the root: the primary worktree that holds the plan. The lane id is the
  member name. `apply` makes and locks each missing lane, and never deletes
  a lane.
- **Each member has a stable address,** for example `shop-api.lane-a`. A
  Claude Code chat binds to the member of its folder. In the other apps, the
  hook line or `AGENTS.md` gives the address.
- **Projects are separate for each session.** A member has no agent queue,
  and sends mail only in its project and to the owner.
- **Only the owner approves and changes the members.** The owner commands
  refuse to run in an agent app or with no terminal.
- **Releases:** 3.4.0 (model, isolation, lanes, folder pin), 3.5.0
  (binding, hook lines), and 3.6.0 (guards, standby), 15 to 19 days [E].

## 2. The problem

In shop-api, a Claude Code supervisor and Cursor and Antigravity
implementers each had a git worktree. mai 3.3.1 cannot give this setup
safely:

- G-1. Only `mai exec -- project seed` makes a project, with one worker (`src/mai/app_cli.py:225-264`).
- G-2. A role is a property of an agent. Two Claude Code chats cannot be supervisor and worker (`postgres_credential_store.py:31-36`).
- G-3. The login reads the roles one time (`log_in_command.py:55`).
- G-4. A board lane holds an agent. `start_item` takes the first item of the agent (`postgres_board_store.py:559-564`).
- G-5. Board notices go to the agent address, which each chat of the app reads (`postgres_board_store.py:241-246`).
- G-6. One agent queue holds the mail of all projects (`mail_rules.py:115-119`).
- G-7. The hook shows the lines of all sessions of the bridge (`cli/internal/hook/notice.go:57-69`, `format.go:94-104`).
- G-8. The board tool schemas do not agree with the board (`tool_schemas.py:444-575`).
- G-9. 30 minutes after the last supervisor call, supervisor mail goes to the agent queue (`postgres_board_store.py:226-239`).
- G-10. No code reads `board.project.repo_path` (`board_migrations.py:15`).

Outside evidence: Claude Code closed the request for MCP resource
subscriptions (issue 7252), thus notices go through hooks and
`wait_for_mail`. Cursor got good results from planners, workers, and a
judge, and Cognition from a reviewer with a clean context. GitHub does not
let an author approve its change. Thus mai has three working roles, and a
holder cannot accept its item. The requirements, with their sections:

- R-1 project from the host CLI (9.1), R-2 role of a session (3.1), R-3
  board lane of a session (6), R-4 notices to sessions (6).
- R-5 one command (9.1), R-6 a call with no session (5.3), R-7 scope of a
  notice (4.3), R-8 presence (7.3).
- R-9 board with no supervisor (7.4), R-10 only the owner approves (8.2),
  R-11 projects stay separate (5.2), R-12 rules of a real project (D-14).
- R-13 folder and app of a lane (3.2, 4.2, 7.3), R-14 pause (7.5), R-15
  guide (9.3), R-16 exclusive session (D-07).

## 3. The model

### 3.1 Projects, members, and roles

The login of a member session is `AGENT.PROJECT-MEMBER`, for example
`cursor.shop-api-lane-a`. Thus one app can be a member of many projects.
Chats and senders use the member address `PROJECT.MEMBER`. The roles, with
their board tools and number:

- `supervisor`: all board tools. Exactly one.
- `worker`: `get_board`, `read_item`, `start_item`, `report_status`, `submit_for_review`. One for each lane.
- `reviewer`: `get_board`, `read_item`, `report_status`, `review_item`. Zero or more.
- `observer`: `get_board`, `read_item`. Zero or more.

A role is a property of a session (D-01), because in shop-api the
supervisor and 8 implementers were Claude Code sessions (G-2). The owner is
not a member, and can call each tool. `board_rules.ROLES`, `TOOL_ROLES`
(`board_rules.py:39`, `:58-68`), and `WORK_ROLES` (`tool_schemas.py:11`)
add `observer`.

### 3.2 The project file

`mai project init` writes the file and `.mai/.gitignore`, `mai project add`
changes the file, and the user commits the two files:

```json
{
  "mai_project": 1,
  "name": "shop-api",
  "goal": "The API of the shop",
  "wip_limit": 2,
  "done_rule": {"checks": [{"name": "check", "required": true}]},
  "members": {
    "supervisor": {"agent": "claude", "role": "supervisor"},
    "lane-a": {"agent": "cursor", "role": "worker"},
    "lane-b": {"agent": "antigravity", "role": "worker"},
    "review": {"agent": "codex", "role": "reviewer"}
  },
  "base": "sha256:..."
}
```

The prototype has no `base` field, thus it refuses this example (A.3). The
folder of a worker is its lane, here
`/Users/me/work/shop-api/.mai/cursor/lane-a`. A different role has the root,
or its relative `folder`. `project.Load` and the server examine these rules:

1. A worker has no `folder` field. For a different role, `folder` is
   relative, not empty, not in `.mai`, and in the worktree of the root. The
   file has no absolute path, no key, and no instance name.
2. `mai_project` is 1. The JSON has no unknown field and no duplicate key,
   because `encoding/json` keeps the last of two equal keys.
3. A member name agrees with `[a-z0-9][a-z0-9-]{0,23}`, and an agent name
   with `[a-z0-9][a-z0-9-]{0,47}`. `PROJECT-MEMBER` has 32 characters or
   less, or the member gives `label`, never in the pool form `s[0-9]+`. The
   names of a worker are path parts, thus never a Windows device name.
4. A project name is never an agent name. `apply` and `AddAgentHandler`
   (`add_agent_command.py:32-40`) examine this rule.
5. The names `supervisor` and `reviewer` are role addresses, for that role
   only. Exactly one member is supervisor.
6. One folder has one member of an agent at most, also a lane.
7. The root is the top folder of the primary worktree (D-20), and not the
   home folder, where `mai uninstall --legacy` removes `~/.mai`
   (`cli/internal/uninstall/legacy.go:126`, `:281`). `Load` examines the
   home folder, and `PlanLanes` the worktree (A.3). `apply` tells the user
   when the root is a submodule, because `git submodule deinit` deletes the
   lanes (A.5).

**The ignore file.** `.mai/.gitignore` has two comment lines and `/*/`,
thus git ignores each lane but not `project.json`. Else `git add -A` adds a
lane as a gitlink (A.3). A commit before the plan has no ignore file, thus
`init` and `apply` also add `/.mai/*/` one time to the file of `git
rev-parse --git-path info/exclude`. `apply` writes a missing
`.mai/.gitignore`, but keeps a different one and gives each lane that `git
check-ignore` does not ignore. The prototype replaces it.

**Worktrees.** In a linked worktree, `project.Find` reads `gitdir` and
`commondir`, and gives the primary worktree if it has the plan. If the root
is on a commit with no plan, `Find` gives the lane, and `Load` gives
incorrect folders (A.3). Thus `Find` in 3.4.0, but not the prototype,
refuses a lane in the plan folder of its primary worktree.

**Copies of an earlier plan.** Each branch and lane has a copy of the plan.
Thus `apply` writes the SHA-256 of the applied plan, with no `base`, in
`base`. The server refuses an owner change (8.2) when `base` is not its
`plan_sha` (`409 PLAN_STALE`).

### 3.3 The tables

Migration 101 (credentials) moves the roles to sessions in
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

For a worker, `folder` is the lane, and the server refuses a different
folder. `apply` stores the root in `board.project.repo_path` (G-10).
Migration 510 (board) links the board lane to its session:

```sql
ALTER TABLE board.project ADD COLUMN remote text, ADD COLUMN plan_sha text,
  ADD COLUMN state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','paused','retired')),
  ADD COLUMN state_note text, ADD COLUMN offline_signal_at timestamptz;
ALTER TABLE board.lane ALTER COLUMN agent DROP NOT NULL, ADD COLUMN branch_prefix text,
  ADD COLUMN login text REFERENCES mail_auth.principal(login);
CREATE UNIQUE INDEX lane_one_login ON board.lane (project, login) WHERE login IS NOT NULL;
CREATE TABLE board.presence (project text NOT NULL REFERENCES board.project(name),
  login text NOT NULL, last_call_at timestamptz NOT NULL, PRIMARY KEY (project, login));
ALTER TABLE board.approval ADD COLUMN action text, ADD COLUMN process jsonb;
```

The app role has no DELETE right. Thus `apply` adds a removed member again
with an UPDATE that sets `removed_at` to NULL, and its session goes back to
`issued`. The UPDATE of agent rows runs before the constraint, thus a role
`owner` cannot stop the migration. `apply` sets `branch_prefix` to
`MEMBER/` (D-16), and the `login` of the board lane of a removed worker to
NULL.

### 3.4 Tools in the project root

A tools test ran each tool in the root of a scratch project with two lanes
(A.4):

| Tool | Result in the root | Precaution |
|---|---|---|
| git 2.54.0 (Apple Git-157) | With `.mai/.gitignore`, `status` and `add -A` skip the lanes. `grep --no-index` reads them, also the lane `.env`. `clean -ffdx` deletes them, but `clean -fdx` keeps them. | `--exclude-standard`. No `git clean -ff`. |
| go1.27.3 | `go list`, `go test`, and `go vet ./...` skip the lanes. `gofmt -l .` and `go work use -r .` use them, thus `gofmt -w .` changes them [A]. | `go fmt ./...` |
| pytest 9.2.0, ruff 0.17.4, ty 0.0.101, TypeScript 6.0.3, Nest CLI 12.0.11 | Skip the lanes. ruff and ty read `.mai/.gitignore`, and the others skip dot folders. | Keep `.*` in `norecursedirs` |
| ESLint v10.14.0 | Lints each lane, thus `--fix` changes them [A]. With the configuration of a real NestJS project, each message shows 3 times. | `{ ignores: ['.mai/'] }` |
| Vitest 5.0.5 | With no configuration, it runs 4 tests in place of 1. | `'.mai/**'` in `exclude` |
| Prettier 3.9.10 | Reads the lanes, thus `--write` changes them [A]. | `/.mai/*/` in `.gitignore` |
| Node.js v26.13.0 | A lane with no `node_modules` uses the packages of the root. | An install in each lane |
| Docker 29.9.2, buildx v0.38.1 | `COPY .` copies 52 of 132 files from `.mai`, with the lane `.env`. | `.mai/*/` and `!.mai/project.json` |
| Docker Compose v5.6.1 | The project name is the folder name, thus lane-a of two projects gets one name and one volume (A.5). | `COMPOSE_PROJECT_NAME=shop-api-lane-a` in the lane `.env` [A] |

Other effects:

- Visual Studio Code and Cursor have no default rule for `.mai` [A]. Add
  `.mai/*/` to `.cursorignore`, and `.mai/*/**` to `files.watcherExclude`.
- Glob of Claude Code does not use `.gitignore`, and a supervisor chat can
  edit a lane with no prompt [A]. A `permissions.deny` rule for `Edit` and
  `Write` in `.mai/*/` stops this, but not Bash. A chat in a lane also reads
  the root `CLAUDE.md` and `CLAUDE.local.md` [A].
- `load_dotenv()` of python-dotenv with no path, direnv, and mise read files
  in parent folders [A]. Thus a lane with no `.env` uses the root `.env`.
- The worktree cleanup of Cursor 3.5 can delete a lane [A] (section 13).
- Each lane adds one checkout and one package install on the disk of the
  root [A]. A link in `.mai` stops `apply` (A.5).
- On Windows, a lane adds 19 to 79 characters to each path (calculated,
  A.4). With no `core.longpaths` and `LongPathsEnabled`, the limit is 259
  [A].

`apply` and the guide give these lines, and the user writes them (D-18).
`apply` and the guide also tell the user when one of these conditions occurs:

- The root has `CLAUDE.local.md`, `.env`, or `.envrc`.
- The root is in iCloud Drive, Dropbox, or OneDrive, which copy each lane.
- On Windows, a lane path and the longest path of `git ls-tree -r --name-only HEAD` have more than 259 characters.

## 4. How a chat finds its member

### 4.1 One form for each folder

`project.Resolve` resolves symbolic links, also for a folder that does not
exist. `apply`, the bridge, and the hook use it. At this time, the bridge
sends `os.Getwd()` with no change (`cmd_runtime.go:49`), but on macOS a chat
after `git worktree add` gives `/private/var/...`. For `darwin` and
`windows`, the CLI and the server compare folded paths (`foldKey`), thus on a
case-sensitive volume, two folders with different case agree.

The match (`MemberOf` in the CLI, `of_folder` on the server) uses the
longest member folder on a path boundary, with two more rules:

1. The chat folder and the member folder have the same top: the nearest
   folder, at or above it, with a `.git` entry. The CLI finds the top with
   no git process, and the lease request sends it [A]. The top of a worker
   is its lane, and the top of a different member is the root.
2. A folder in the plan folder gives only a lane.

For example, `shop-api/src` gives the supervisor,
`shop-api/.mai/cursor/lane-a/src` gives lane-a, and `shop-api-old` gives no
member. A Claude Code chat gets no member in a Cursor lane, in
`shop-api/.mai`, or in `shop-api/.claude/worktrees/NAME`. It also gets no
member in a nested clone, a submodule, or a nested project. The prototype
has only rule 2, thus it gives the supervisor in `.claude/worktrees/NAME`
(`project_test.go:323`).

### 4.2 Claude Code: the folder binding

Claude Code starts one bridge for each chat process, in the folder of the
chat. Thus `PER_CHAT_APPS = ("claude",)` (D-06):

```text
claude (cwd /Users/me/work/shop-api)
  -> mai mcp --agent claude     folder = Resolve(Getwd), top
  -> POST /api/leases {app: "claude", os: "darwin", folder, top, pid, bind: true}
server: of_folder("claude", folder, top) = claude.shop-api-supervisor
  -> lease on that session, result {session, bound: "folder", member: {...}}
```

1. The bridge sends `bind: true` only for its first lease. If a different
   bridge holds the member, the result is a pool session with `member_busy`.
2. Leases are in memory (`sessions_module.py:39`). After a restart, a bound
   bridge gets a lease again with its member login (`lease.go:405`), and a
   pool bridge with `bind: false`. If the member is busy then, each call
   gives `SESSION_BUSY`, and the bridge does not go to the pool.
3. `leaseOf` (`session.go:482`) maps the login and the address of the bound
   member to the bound lease. Else an explicit `session` gets
   `SESSION_BUSY` (`lease_service.py:503`).

Codex goes into `PER_CHAT_APPS` after a test of its threads. The code
changes:

- `LeaseService._acquire`: on `bind: true` from an app in `PER_CHAT_APPS`,
  call `CredentialService.member_of_folder(agent, folder, top, os)`.
- `guideAppOfProc` (`cmd_guide.go:503`) moves to `procchain.App` and adds
  `agy`, which gives `copilot` in a Code terminal at this time (R-13).
- `bridge.go` keeps `bound` and `member` for `whoami` and the notice file.

### 4.3 Shared bridges: the hook line

One Cursor bridge served all windows, thus the hook tells each chat its
member:

```text
mai: this chat is shop-api.lane-a (worker in project shop-api).
Give session: "shop-api.lane-a" in each mai tool call. 2 new mail.
Read only the mail of shop-api.lane-a.
```

In Claude Code, the second sentence is "Your folder gives your session. Give
no session." The method:

1. Each heartbeat gives the new-mail count of each member of the agent
   (`ImapNewMailCounter`), also with no lease in this bridge.
2. The hook finds the folder: the bound member in Claude Code, because `cwd`
   can change after a `cd` [A]. `workspace_roots[0]` in Cursor [A], and
   `cwd` in Codex and Copilot [A]. A `file://` URI becomes a path.
3. `hook.Lines` gives only the line of that member. It gives the line at
   the start and at each prompt, but at a stop and at `PostToolUse` only for
   new mail. With no member, the output stays as in 3.3.1.

Copilot has no prompt hook, and 3.5.0 adds `userPromptSubmit` [A].
Antigravity has many hub processes and no folder field [A]. Thus its member
gets its address only from `AGENTS.md`, in the block of `mai project init
--agents-md`.

### 4.4 The session argument

A chat can give its session as the member address `shop-api.lane-a`, the
login `cursor.shop-api-lane-a`, or the label `shop-api-lane-a`. A name with
a dot whose first part is not the agent of the bridge is a member address.
The server resolves it to the login of a member of that agent, else gives
`SESSION_UNKNOWN`. A member name alone is a label. The changes are in
`lease_service.py:_named_session`, `session.go:leaseOfLogin`, and `openOnce`
(`session.go:334`).

## 5. Addresses and isolation

### 5.1 Member and role addresses

Each member and role address is a row of `mail_auth.alias`, which Postfix
and the mail UI read (`etc/postfix/pgsql-alias.cf`). `apply` writes the rows
in the transaction of the members:

- `shop-api.supervisor` goes to the supervisor member.
- `shop-api.reviewer` goes to the reviewer if the project has exactly one, else the supervisor.
- `shop-api.MEMBER` goes to that member. The address of a removed member goes to `owner`.

The sender of a member mail is its login, because Postfix and
`sender_proved` (`message.py:224`) accept only the SASL login as From. Thus
each member mail gets `Reply-To: PROJECT.MEMBER`. `parse_summary`
(`message.py:241`) trusts `X-Mai-Project` from a proved member sender.

### 5.2 The rules of isolation

Isolation is for each session. A chat in a shared bridge can give the
address of each member of its agent, because `_named_session` examines only
the agent (`lease_service.py:473-504`). Section 8 gives the guards. With the
member flag of `SessionState`, these parts use no agent queue:

- `mail_rules.queue_of`, `mail_reader.queue_of`, `notice_lines.mailboxes_of`: only the INBOX of a member.
- `NewSessionHandler` (`new_session_command.py:77`) and `GrantMailboxAccessHandler` (`grant_mailbox_access_command.py:49-55`): no queue grant for a member, also at a start of Dovecot.
- `RevokeSessionHandler` (`revoke_session_command.py:32-42`): unread mail of a member goes to `owner`.
- `MailSender.check_known`: after alias resolution, a member sends only in its project and to `owner`, else `RECIPIENT_OUT_OF_PROJECT`.
- `list_addresses_query.py:16-35`: a member sees its project and `owner`.
- `mail_sender.build`, `Call.resolve_project`: `X-Mai-Project` on each member mail. A board call acts only in the project of the session.

When a session becomes a member, `apply` removes its queue ACL first, or
stops. Mail between projects is not possible (D-05).

### 5.3 A call with no session (R-6)

In the 3.1.0 smoke test, five Cursor chats gave no session and read the pool
INBOX. Thus:

1. A call with no session gets `SESSION_REQUIRED` (path `session`) when its
   `ref` is in the mailbox of a named session of the same agent.
2. `mai agent pool off AGENT` closes the pool (D-15). Then each call with no
   session from a bridge that is not bound gets `SESSION_REQUIRED`.

## 6. The task flow

The board states stay the same (`board_rules.py:43-53`). Each step gives
the caller, the tool, the state change, and the notice:

1. Supervisor: `plan_item` (key, title, spec, lane, reviewer), `backlog` to `ready`, no notice.
2. Supervisor: `assign_item` (item, lane), `ready` to `assigned`, to the session of the board lane.
3. Worker: `start_item` (no item), `assigned` to `in_progress`, to the supervisor.
4. Worker: `report_status` (text, state, cause), `in_progress` or `blocked`, to the supervisor.
5. Worker: `submit_for_review` (evidence), `in_progress` to `in_review`, to the reviewer and the supervisor.
6. Reviewer: `review_item` (changes, findings), `in_review` to `changes_requested`, to the holder.
7. Reviewer: `review_item` (accept, no merge commit), stays `in_review` with the signal `review_accepted`, to the supervisor.
8. Supervisor: `review_item` (accept, merge commit), `in_review` to `done`, to the holder and the reviewer.

The input `lane` is the member name of a worker. The code changes, in
`postgres_board_store.py` if no file is given:

- `Call.resolve_project` and `whoami` read the role of `st.login` in the
  call (G-3), with no role filter of the login time in the dispatcher
  (`dispatcher.py:260-261`, `:199-201`).
- `_assign_item` sets `assignee` to the login of the board lane.
  `_start_item` with no item takes the first item of the board lane of the
  caller (R-3). An item of a different board lane gives `FORBIDDEN_ROLE`,
  and the WIP count adds `AND project = %s`.
- `_start_item` does not call `claim_thread` (`:571`) for a member, because
  `ClaimService` reads the agent queue (`claim_service.py:65-72`). The lock
  is `item.holder` with `FOR UPDATE SKIP LOCKED` (D-11). `ClaimService` and
  `SessionStaleJob` (`session_stale_job.py:84-91`) skip member logins.
- `item_targets` is the holder, then the session of the board lane, then the
  supervisor, else `owner` (R-4). `take_supervisor` records only presence.
- `_review_item` compares the reviewer login with `st.login`, not
  `st.agent` (`:918`), and `_get_board` shows `lane.login` (`:427-441`).
  The holder or an evidence actor of the item gets `OWN_ITEM`, the
  supervisor included. A reviewer accept with the merge commit sets `done`
  for an item of the supervisor (D-09).
- `_submit_for_review` sends to the item reviewer, else to
  `shop-api.reviewer`, with a copy to the supervisor. A board mail to a
  worker or reviewer gets the `Reply-To` of the supervisor.

## 7. Notices, standby, and an offline supervisor

### 7.1 Notice lines

The heartbeat result (`lease_service.py:219-240`) adds up to three entries
`{kind, from, item}` of new mail, with no subject, because an agent writes
the subject. A hook line then tells, for example, `1 request from
shop-api.supervisor, item itm-abcdefghijkl`.

### 7.2 The chat that waits

1. **All apps:** the chat calls `wait_for_mail` with `timeout_sec` 50, 30
   times at most, then tells the user. Each loop costs one model turn [E].
2. **Claude Code, 3.6.0:** a `Stop` hook with `asyncRewake` [A] runs
   `mai hook --wait`, which reads the notice file each 2 seconds. On new
   mail, it writes the line and exits with code 2, and Claude Code starts
   the chat again.
3. **Cursor:** a stop hook that holds the chat is not safe [A], thus Cursor
   uses method 1.

### 7.3 Presence on the board

`get_board` gives, for each member (R-8, R-13):

```json
{"member": "lane-a", "role": "worker", "folder": "/Users/me/work/shop-api/.mai/cursor/lane-a",
 "lease": {"app": "cursor", "pid": 3301, "bridge_folder": "/Users/me/work"},
 "last_call_at": "2026-11-14T10:02:11Z", "waiting": true, "current_item": "itm-abcdefghijkl"}
```

The dispatcher records each call of a member in `board.presence`, one write each 60 s at most. `waiting` is true
during an open `wait_for_mail`.

### 7.4 An offline supervisor

The mail of the supervisor stays in its INBOX while no chat runs. A new job
`board/jobs/supervisor_offline_job.py` runs each 60 seconds, and reads the
`last_call_at` of the supervisor and the age of the items in `in_review` or
`blocked`. When the two are 30 minutes or more, the owner gets one `signal`
mail. The job sets
`offline_signal_at`, and sends again only after a subsequent call (D-08).

### 7.5 Pause

`mai project pause shop-api --note TEXT` sets `state` to `paused`. Then
`assign_item` and `start_item` give `PROJECT_PAUSED`, and each member gets
one notice with the note (R-14). Reports and reviews continue.
`mai project resume` sets `active`.

## 8. A chat that gives the member of a different chat

### 8.1 Proofs and guards

The agent key proves the agent, and the lease token proves the bridge. No
proof shows which chat in one bridge made a call, or the app and the folder
that the bridge reports (`leases_controller.py:104-119`). MCP stdio has no
chat identifier, thus the threat is the error of a chat. The guards:

1. **One lease for each member** (3.3.1). A second bridge gets
   `SESSION_BUSY`. Keep one chat for each Cursor window.
2. **Folder pin** (3.4.0). A bridge of an app in `PER_CHAT_APPS` gets a
   member only from the folder and the top of that member, else
   `SESSION_FOLDER`. The folder is not proved, thus the pin stops only an
   error.
3. **Cursor hook guard** (3.6.0). A `beforeMCPExecution` hook [A] refuses a
   `tool_input.session` that is not the member of the chat folder.
4. **Audit.** Each board event records the login, the app, the PID, and the
   bridge folder.

Codex, Copilot, and Antigravity have no guard, and D-13 gives a hard limit.

### 8.2 Only the owner approves (R-10)

Each program of the OS user can read the owner key, thus a chat with a shell
tool can run `mai project approve`. An owner change adds or removes a
member, or changes a role, agent, label, folder, `done_rule`, `wip_limit`,
or the root. It can give a chat the right to approve or accept an item.
Thus `approve`, `pause`, `resume`, `retire`, `mai agent pool off`, and an
`apply` with an owner change refuse to run in two cases:

- An agent app is in the parent process chain (`guideAppOf`,
  `cmd_guide.go:489`).
- The stdin is not a terminal.

In a terminal, the command must get a typed word, or `--yes`. The approval
row records the action and the process chain. A program that calls the owner
API can skip these checks. For agents, only an approval row approves, and
only its subject.

## 9. The CLI and the owner API

### 9.1 Commands

- `mai project init NAME [--goal TEXT] [--supervisor AGENT] [--agents-md]`: writes the file and `.mai/.gitignore`, and adds `/.mai/*/` to `info/exclude`.
- `mai project add MEMBER --agent A --role R [--folder DIR]`, `mai project remove MEMBER`: change the file only. A worker gets no `--folder`.
- `mai project apply [--file PATH] [--ref REF] [--dry-run] [--lanes] [--move-root] [--items return|to MEMBER] [--yes]`: makes the server agree with the file, makes the missing lanes, then writes `base`. `--lanes` makes only the lanes.
- `mai project list`, `mai project show NAME` (`--json`): projects, members, lanes, leases, presence, signals.
- `mai project pause NAME --note TEXT`, `mai project resume NAME` (7.5), `mai project retire NAME`, `mai project approve NAME --item KEY --note TEXT`, and `mai agent pool off|on AGENT` (5.3).

Thus the owner makes a project with `mai` only (R-1). `apply` refuses an
unknown agent, makes the member sessions with no queue grant, then writes
the project, members, board lanes, and aliases in one transaction. A
removal revokes the session.

**A removed worker.** When a removed worker holds an item in `assigned`,
`in_progress`, `changes_requested`, or `blocked`, `apply` refuses (`409
ITEMS_HELD`). With `--items return`, each such item goes back to `ready`
with no holder. With `--items to MEMBER`, it goes to `assigned` for that
worker.

**The root.** An `apply` from a root that is not `repo_path`, for example
from a second clone, gets `409 ROOT_CHANGED`. After a move, `apply
--move-root` changes the root and each member folder. `retire` removes each
member, and thus releases the folders.

**Lanes.** Before the transaction, `PlanLanes` gives the state of each lane,
and `apply` resolves `--ref` and examines the ignore file. After it,
`MakeLanes` makes each missing lane with `git worktree add --lock --reason
"mai lane PROJECT.MEMBER" --detach -- PATH COMMIT` (D-16). It makes the
parent folder first, because else git 2.54.0 can register a path two times
(A.3). If a lane step gives an error, `apply` gives exit 1 and writes no
`base`, and the next `apply` makes the missing lanes.

The lock keeps the entry of a moved lane from `git worktree prune` (A.5)
and `git gc` [A], thus `apply` also locks each lane with no lock. With
`.gitmodules`, `apply` gives `git submodule update --init --recursive` for
each new lane. The states:

- `missing`, no folder: `apply` makes the lane.
- `ok`, a clean worktree of the root repository: nothing.
- `dirty`, uncommitted or untracked paths: nothing, but it gives the count.
- `moved`, the `.git` file names `OLD/.git/worktrees/ID`, and the root has `.git/worktrees/ID`: `git worktree repair PATH` with `--move-root`, else a stop.
- `registered-but-missing`, git lists the path, but the folder is missing: `git worktree add -f -f --lock`, because the entry is locked.
- `not-a-worktree` (a plain folder, a file, or a link, also in a parent folder) and `worktree-of-a-different-repository`: a stop before a change.
- `kept`, a worktree in `.mai/*/*` of `git worktree list --porcelain -z` that is not a lane of the file: nothing, but it gives the dirty count (D-19).

When one `apply` removes a worker and adds a worker with the same agent or
member name, `apply --dry-run` gives `git worktree unlock OLD` and `git
worktree move OLD NEW`. Else the new lane cannot use the item branch of the
earlier lane (A.5). `apply` never runs `git worktree prune`, which removes each
missing worktree, and never deletes a folder. A second run gives `No
change.` (R-5). The lane steps use git 2.36 or a subsequent version [A].

**A move of the root.** Close the apps, move the folder, then run `apply
--move-root`. Until then, the folder binding gives no member.
`worktree.useRelativePaths` (git 2.48 [A]) keeps a moved lane correct, but
sets `extensions.relativeworktrees=true` (A.5), which an earlier git or a
libgit2 tool can refuse [A]. Thus mai does not set it.

**Who runs each step.** An agent app can run `init`, `add`, `remove`, the
guide, `apply --dry-run`, `apply --lanes`, and an `apply` with no owner
change. In a lane, `init`, `add`, `remove`, and `apply` refuse to run (`run
it in the root`), because `Find` gives the plan of the root. Only the owner
deletes a lane, with `git worktree unlock` and `git worktree remove`.

### 9.2 Owner API

For each route, the owner key is necessary. The controller
`board/api/projects_controller.py` calls a new `ProjectService` with
`plan`, `apply`, `list`, `show`, `set_state`, and `approve`.

- `PUT /api/projects/{name}?dry_run=1` with `{"file", "root", "remote", "folders", "os", "move_root", "items"}` gives `200 {"changes": [...], "applied": false, "plan_sha"}`.
- `GET /api/projects`, `GET /api/projects/{name}` gives `200`.
- `POST /api/projects/{name}/state` with `{"state", "note", "process"}` gives `200 {"state", "notices"}`. `retired` removes each member.
- `POST /api/projects/{name}/approvals` with `{"subject_kind", "item", "decision", "note", "process"}` gives `201 {"approval"}`.

The errors are `400 BAD_PLAN` (with `path`), `404 AGENT_UNKNOWN`, and `409
NAME_TAKEN`, `LABEL_TAKEN`, `FOLDER_TAKEN`, `PLAN_STALE`, `ROOT_CHANGED`, or
`ITEMS_HELD`. The CLI sends absolute folders and makes the lanes, because
the server in the container cannot read the host folders. After migration
101, `project seed` gives an error, because `app_cli.py:256` writes the
column `agent` (D-10).

### 9.3 `mai guide --project`

The command finds the project file from the current folder, and changes
nothing (R-15). It gives the state and the steps that are not done:

```text
Project shop-api: file found, not applied.
Steps:
  1. mai agent add codex          (the agent codex does not exist)
  2. mai project apply            (makes the lanes lane-a and lane-b)
  3. Open a chat in /Users/me/work/shop-api/.mai/cursor/lane-a (member lane-a, Cursor).
  4. Add .mai/*/ and !.mai/project.json to .dockerignore.
  5. Add COMPOSE_PROJECT_NAME=shop-api-lane-a to the .env of lane-a.
This folder: member supervisor (claude).
```

The code is a new `guideRun.project` step in `cmd_guide.go`, next to
`lanes()` (`:965`), with `PlanLanes`. The same release changes the text of
`lanes()` (`:972`), which calls a named session a lane.

## 10. The MCP contract

Contract 3.4.0 comes with mai 3.4.0 (`json_rpc.py:16`). The tool count stays
22.

- `whoami`: adds `project` (name, member, role, lane, address, supervisor, folder, state). 3.5.0 adds `bound` and `member_busy`.
- `list_addresses`: adds `project` with its members and role addresses.
- `send_message`, `reply`: accept member and role addresses.
- `list_inbox`, `wait_for_mail`: each summary adds `project` and the member address of the sender.
- `get_board`: members in place of agents, and presence in 3.5.0.
- `plan_item`: inputs `key`, `title`, `spec`, `lane`, `reviewer` (a member name), `state` (G-8).
- `assign_item`: inputs `item`, `lane` (necessary), `sprint`. No `agent`.
- `start_item`: `item` is optional, a ref or a key (`postgres_board_store.py:178-189`). No `lease_until` and no `moved`.
- `report_status`, `review_item`: inputs `item`, `text`, `state`, `cause`. A finding is a string or `{severity, text}`.
- All board tools: `project` is optional, and must be the project of the session.

The new errors are `NOT_A_MEMBER`, `PROJECT_PAUSED`,
`RECIPIENT_OUT_OF_PROJECT`, `OWN_ITEM`, `SESSION_FOLDER`, `SESSION_REQUIRED`,
and `PLAN_STALE`. `SESSION_ARG` (`tool_schemas.py:37`) adds the member
address form. The section "Work in a project" of `agent_guide.md` tells a
chat to call `whoami`. Outside Claude Code, the chat gives its member
address in each call. Only an approval row is approval.

The docs change in `docs/reference/`, `docs/explanation/architecture.md`,
and a new `docs/how-to/run-a-project.md`. The how-to gives the lines of
section 3.4, a move of the root, and a change of a member name (9.1).

## 11. Migration from the current state

On 14 November 2026, the instance of shop-api has no agent and no project,
thus migration 101 finds no row. The workers use sibling worktrees such as
`../shop-api-cursor`. The steps after the upgrade to 3.4.0, in the root:

1. `mai agent add` and `mai connect` for claude, cursor, and antigravity.
2. `mai project init shop-api --supervisor claude --agents-md`, then
   `mai project add lane-a --agent cursor --role worker`, and lane-b.
3. Close the apps and the servers of each sibling worktree. Run
   `mkdir -p .mai/cursor` and `git worktree move ../shop-api-cursor
   .mai/cursor/lane-a`, and do the same for lane-b. Git does not make the
   parent folder (exit 128). The branch and the uncommitted files stay.
4. Commit `.mai/project.json`, `.mai/.gitignore`, and `AGENTS.md` on `main`.
   Run `mai project apply`, which locks the moved lanes, and commit the
   `base` that it writes. In a moved lane, `git merge main` gives the new
   commit.
5. Open each app again in its lane. The chat history stays with the earlier
   folder [A]. Make each `.venv` again, because its scripts name the earlier
   path (A.5). Put a `.env` in each lane.
6. The supervisor changes the task skill, with the member addresses and the
   lanes. Each chat gives its address from `AGENTS.md`, and after 3.5.0
   from the hook line.

With submodules, `git worktree move` refuses (A.5). Then commit or push the
work of the sibling, and run `git worktree remove --force` on it. Then
`apply` makes the lane, and the worker runs `git switch BRANCH` and `git
submodule update --init --recursive` in it. The sub-agents of the
supervisor do not use the mai tools.

## 12. Rollout

- 3.4.0, L, 10 to 12 days [E]: sections 3, 4.1, 4.4, 5.1, 5.2, 6, 7.5, 8.2, 9.1, 9.2, and 10, with the docs.
- 3.5.0, M, 4 to 5 days [E]: sections 4.2, 4.3, 5.3, 7.1, 7.3, 7.4, and 9.3, with the Copilot prompt hook.
- 3.6.0, S, 1 to 2 days [E]: the Cursor hook guard, and the standby hook of Claude Code.

The lanes and their repair steps add 3 days to 3.4.0 [E]. Each release is
one pull request, and the user merges it.

## 13. The test plan

Unit tests use the memory backend, and integration tests use PostgreSQL
and Dovecot in a test container (`MAI_IT`). The 12 lane tests of A.3 go into
3.4.0, and `TestPlanLanesGivesTheStateOfEachLane` adds a lane path that is
a file. The new tests:

- 3.2: `TestTheExampleOfTheDesignLoads`,
  `test_apply_refuses_an_owner_change_from_a_copy_of_an_earlier_plan`,
  `test_a_project_name_is_not_an_agent_name_in_each_order`,
  `TestFindRefusesALaneWhenTheRootHasNoPlan`,
  `TestApplyWritesTheExcludeLineOneTime`, and
  `TestApplyKeepsADifferentIgnoreFileAndGivesEachLaneThatGitDoesNotIgnore`.
- 3.3: `test_apply_adds_a_removed_member_again`,
  `test_apply_refuses_a_worker_folder_that_is_not_its_lane`, and
  `test_apply_sets_the_branch_prefix_of_a_board_lane`.
- 4.1 and 4.2: `TestMemberOfGivesNoMemberInADifferentWorktreeInTheRoot`,
  `test_of_folder_gives_no_member_in_a_different_worktree_in_the_root`,
  `test_of_folder_gives_no_member_of_a_parent_project_in_a_nested_project`,
  `TestBridgeMapsTheMemberAddressToTheBoundLease`, and
  `TestABoundBridgeKeepsItsMemberAfterARestartOfTheLeaseStore`.
- 6: `test_start_item_of_a_member_with_no_queue_right` (real
  Dovecot), `test_a_reviewer_session_can_review_its_item`, and
  `test_the_supervisor_cannot_accept_an_item_that_it_started`.
- 8.2: `TestApplyThatAddsAReviewerRefusesToRunInAnAgentApp`,
  `TestPoolOffRefusesToRunInAnAgentApp`, and
  `TestProjectCommandsRefuseToRunInALane`.
- 9.1: `TestApplyWithABadRefChangesNoServerState`,
  `TestApplyStopsBeforeAChangeWhenALaneIsNotAWorktree`,
  `TestPlanLanesGivesKeptForALaneThatIsNotInThePlan`,
  `TestApplyGivesMovedAndTheRepairCommandAfterAMoveOfTheRoot`,
  `TestMakeLanesMakesARegisteredButMissingLaneAgainWithOneEntry`,
  `TestMakeLanesLocksEachLane`,
  `TestApplyGivesTheMoveCommandsForANewMemberName`,
  `test_apply_refuses_to_remove_a_worker_with_an_item_in_progress`,
  `test_apply_from_a_different_root_gives_root_changed`, and
  `test_retire_releases_the_folders_of_a_project`.
- 10: `test_a_start_item_with_no_item_gets_through_the_schema`.
- R-5: `test_a_second_apply_of_the_same_file_makes_no_change`. R-6:
  `test_a_ref_of_a_named_session_with_no_session_gives_session_required`.
- R-10, R-13, R-14, R-15: `TestApproveRefusesToRunInAnAgentApp`,
  `TestProcChainGivesAntigravityForAgyInCode`,
  `test_start_item_gives_project_paused`, and `TestGuideProjectChangesNothing`.
- R-11: `test_a_member_gets_no_queue_right_after_a_start_of_dovecot` and
  `test_a_member_cannot_send_to_a_different_project`.

An end-to-end check on a scratch instance (ports 18880 to 18889) uses a
supervisor chat in the root and two worker chats in lanes. It records the
hook lines and the notice targets of one item, with one restart of the
container. A test of Cursor 3.5 shows if its worktree cleanup deletes
`.mai/cursor/lane-a`. On Windows, a test makes a lane with a path of more
than 259 characters.

## 14. Decisions for the user

The recommendation of each decision is in italics.

- **U-01.** Lanes are git worktrees at `<project-folder>/.mai/<agent>/<lane-id>`. *The user made this decision on 14 November 2026.*
- **D-01.** Roles are properties of sessions, not agents. *Yes. Two Claude Code roles are necessary.*
- **D-02.** A session is a member of one project at most. *Yes. Else each mail tool must get a `project` argument.*
- **D-03.** The plan is committed, and the CLI reads it in the primary worktree. *Yes. Then the guide can find a project.*
- **D-04.** Addresses `PROJECT.MEMBER` through `mail_auth.alias`. *Yes. Postfix and the mail UI use that table.*
- **D-05.** No mail between projects. *Yes.*
- **D-06.** Folder binding and the folder pin for Claude Code only. *Yes. Codex goes in after a test of its threads.*
- **D-07.** No exclusive members (R-16) until they are necessary. *Yes. In 3.1.0, chats lost the handle.*
- **D-08.** No second supervisor. *Yes. One user keeps the scope and the push.*
- **D-09.** The supervisor sets done with the merge commit. *Yes. In shop-api, the supervisor merges.*
- **D-10.** `project seed` goes in 3.4.0. *Yes. No project exists.*
- **D-11.** Items use `item.holder` as the lock, not thread claims. *Yes. A member has no agent queue.*
- **D-12.** Standby of Claude Code with `asyncRewake` in 3.6.0. *Yes, after a check of `asyncRewake`.*
- **D-13.** A hard limit for a shared bridge: one agent for each app and project, for example `cursor-shop-api` [A], with lanes in `.mai/cursor-shop-api`. *Document it, not as the default.*
- **D-14.** R-12: finding severities in 3.4.0, and the other rules in a different proposal. *Yes. They do not change the identity of sessions.*
- **D-15.** `mai agent pool off` is an owner command, and `apply` does not set it. *Yes. Chats outside a project use the pool.*
- **D-16.** A new lane has a detached HEAD. The worker makes one branch for each item, with the `branch_prefix` of its board lane. *Yes. Then mai makes no branch.*
- **D-17.** A reviewer lane (a `lane` field) comes after 3.6.0. *Yes. Until then, a reviewer reads a branch with `git show`, and keeps the checkout of the root.*
- **D-18.** mai writes only `.mai/project.json`, a missing `.mai/.gitignore`, the line in `info/exclude`, and the marked block in `AGENTS.md`. *Yes. The user reviews each committed change.*
- **D-19.** The lane of a removed member stays as `kept`, and the owner unlocks it before `git worktree remove`. *Yes. A lane can hold uncommitted work.*
- **D-20.** 3.4.0 refuses a project in a subfolder of a repository. *Yes. A lane is a worktree of all the repository.*
- **D-21.** mai locks each lane, and keeps absolute worktree paths. *Yes. Relative paths change the repository format.*
- **D-22.** A folder gives a member only in the worktree of that member (4.1). *Yes. Else a chat in `.claude/worktrees` can take the supervisor.*
- **D-23.** A project has one root, and a second clone uses a different project name. *Yes. Else one member gets two lanes.*

## Appendix A. Prototype evidence

Each prototype starts at `b1880ac`, and installed no binary and touched no
live instance. The prototype branches are not pushed. `b0abac4` has test
data that is not for this repository, thus a push of `proto/projects-cli`
gets one new commit with the tree of `cba1e71`.

### A.1 Go CLI: the member of a chat folder

Branch `proto/projects-cli`, commit `b0abac4`: new
`cli/internal/project/project.go`, and changes to `cli/internal/hook`. 13
new tests proved `Find` from a subfolder and a linked worktree, the `Load`
rules (18 broken rules), `MemberOf`, and the hook rules. With no folder, the
hook output of 10 format and event pairs is the same as in 3.3.1. The
commands ran in `cli`, with `HOME`, `GOCACHE`, and the configuration folders
in a scratch folder and `GOPROXY=off`. They were `gofmt -l .`, `go vet
./...`, and `go test -count=1` of the two packages and of `./...`, each with
its exit code and a `grep -c FAIL` of its log.

Results: no `gofmt` file, 23 tests that pass (13 new), 0 `FAIL`, 0
skipped, `go vet` exit 0, and 17 packages that pass.

### A.2 Python server: roles and lanes of sessions

Branch `proto/projects-server`, commit `7b23c5f`, changes the two
migrations, the board and credential stores, `log_in_command.py`, and
`dispatcher.py`. Proved with PostgreSQL 18.6, in
`tests/modules/board/test_session_roles_it.py`:

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

The fixture used a fake mailer and a fake claims service. Thus it did not
use the tool schema or the agent queue, and the review found an error in
each (sections 6 and 10). The container `mai-wf-proj-srv`
(`ghcr.io/monorithm/mai:3.3.1`, `--pull=never`) ran PostgreSQL. On the host,
`V` is the `bin` folder of the primary venv, and `HL` holds the locked
wheels of `jsonschema`:

```sh
PYTHONPATH=src:$HL $V/python -m pytest -p no:cacheprovider -q; echo "exit=$?"
$V/ruff check --no-cache src tests; $V/ruff format --no-cache --check .; $V/ty check --python "$V/.." --extra-search-path $HL
```

Results: `tests/modules/board` gave 30 passed and 1 skipped (`MAI_IT`). The
host suite gave 588 passed and 46 skipped, also `test_postgres_round_trip`
and `test_stdio_login_whoami_logout`. `ruff`, `ruff format`, and `ty` gave
exit 0. No container or volume stayed. With no `HL`, the host command gives
3 collection errors, also on the base, but `main` has no defect. The primary
checkout was on `feat/named-sessions` (`10beea9`), before release 3.1.1
(`99aebc1`) added `jsonschema` to `pyproject.toml` (`:20` at `b1880ac`).

### A.3 Go CLI: lane worktrees in the plan folder

Branch `proto/projects-cli`, commit `cba1e71` on `b0abac4`: new
`cli/internal/project/lanes.go` (`PlanLanes`, `MakeLanes`, `EnsureIgnore`),
and changes to `project.go` (`Load`, `LanePath`, `MemberOf`, the home
check). Tools: git 2.54.0 (Apple Git-157) and go1.27.3 darwin/arm64. 10 of
the 12 tests use `t.TempDir`, real commits, and a real `git worktree add`,
with no git configuration of the user. Two tests examine only the name and
folder rules. Proved:

- `TestLoadGivesEachWorkerTheFolderOfItsLane`: rule 1 of section 3.2.
- `TestTheNameRulesGiveSafePathParts`: the member rule and the agent rule
  each accept 26 of 820 short names, and each is a safe path part.
- `TestPlanLanesGivesTheStateOfEachLane`: five of the six states of the
  prototype, with no change to `git status --porcelain --ignored`.
  `TestGitCleanOfTheRootAndTheLanes` gives `registered-but-missing`. No test
  makes a lane path that is a file.
- `TestMakeLanesMakesOnlyTheMissingLanes`: two detached lanes, a dirty lane
  and a plain folder kept byte for byte, and no change in the second run. A
  bad ref to `MakeLanes` writes no file.
- `TestTheIgnoreFileKeepsTheRootClean`, `TestANestedLaneWithNoIgnoreFile`:
  `git check-ignore -v` gives `.mai/.gitignore:3:/*/` for each lane, and
  exit 1 for `project.json`.
- `TestGitCleanOfTheRootAndTheLanes`, `TestGitWorktreeRemoveRefusesADirtyLane`,
  `TestGitWorktreeMoveOfASiblingWorktreeGivesTheLane`,
  `TestPlanLanesRefusesARootThatIsNotThePrimaryWorktree`,
  `TestFindAndMemberOfFromInsideALane` (also through a link and in upper
  case), and `TestTheHomeFolderIsNotARootOfAProject`.

Git tests:

- On a commit before the plan, `git add -A` stages a lane as a gitlink. With
  `/.mai/*/` in `.git/info/exclude`, `git status` is clean.
- With the root on a commit with no plan, `Find` from a lane gives the lane,
  and `Load` gives folders such as `.mai/cursor/lane-a/.mai/cursor/lane-a`.
- After `git clean -ffdx`, `git worktree add` of a registered path with no
  parent folder makes a second entry.

Mutation: the removal of each of 8 guards gives at least one `FAIL`. With
the commands of A.1: no `gofmt` file, 35 tests that pass, 0 `FAIL`, 0
skipped, and exit 0 for `go vet` and `go test ./...`. The prototype does not
have the `base` field, the lock, the states `moved` and `kept`, rule 1 of
4.1, or the ignore rule of 3.2.

### A.4 Tools in the root of a project with lanes

The scratch repository `shop-api` had Go, Python, TypeScript, ESLint, and a
`Dockerfile` with `COPY . /ctx/`, on macOS 26 (Darwin 25.6.0), arm64. Two
lanes came from `git worktree add --detach`, and lane-a got a test `.env`
file and one error for each tool. The Node tools came from the read-only
`node_modules` of a real NestJS project. Node and Python ran in
`sandbox-exec`, with no network. The Docker builds used `--pull=false`. No
test: ripgrep, Jest, watch modes, mypy, pyright, uv, workspaces, and
Windows, where the path lengths are calculated.

### A.5 Review tests

Scratch scripts with git 2.54.0 and no git configuration of the user, and
Go tests on a copy of `cba1e71`:

- After a move of the root, git in a lane gives `fatal: not a git
  repository: (null)`. `git worktree repair` with no path does not repair
  the lane, but `git worktree repair .mai/cursor/lane-a` does. After a `git
  worktree prune`, the repair is not possible, but a locked lane keeps its
  entry.
- With `--relative-paths`, a lane stays correct after a move, and git sets
  `core.repositoryformatversion=1` and `extensions.relativeworktrees=true`.
- For a registered path, `git worktree add` gives exit 128, and `-f` (`-f
  -f` for a locked lane) gives one entry and a correct lane.
- `git worktree move` and `remove` refuse a worktree with a submodule, and
  `remove --force` removes it. In a superproject, `git submodule deinit` of
  a root deletes its lane, with an uncommitted file.
- After a change of a member name, the new lane cannot use the item branch
  of the earlier lane. `git worktree move` before `apply` keeps it.
- `docker compose config` (Docker Compose v5.6.1, no pull) gives the name
  `lane-a` and the volume `lane-a_pgdata` for lane-a of two projects.
- In a moved `.venv`, `pip3` gives `bad interpreter` (exit 127).
- With `.mai/cursor` a link to a different disk, `PlanLanes` gives
  `not-a-worktree`.
- `MemberOf` gives the supervisor in `.claude/worktrees/NAME` and in a
  nested clone. On the server, a match on all projects of the agent gives
  the supervisor of the parent project in a nested lane.

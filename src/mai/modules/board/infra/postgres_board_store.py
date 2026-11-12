"""The PostgreSQL board store: Scrum work and supervision on top of mail threads.

State changes go to PostgreSQL in one transaction with their history row. Board mail goes from
board@mai.internal after the commit. A failed board mail does not undo a state change; the result
then has notice_sent false. The SQL code is synchronous; the async port runs it in a worker thread.
"""

import datetime as dt
import json
import logging
from functools import partial
from typing import Any

import anyio
import anyio.from_thread
import anyio.to_thread

from mai.platform.contract import ToolError

from ..domain import board_rules as B
from ..domain.board_store import BoardStore

log = logging.getLogger("mai.board")

SUPERVISOR_LEASE = dt.timedelta(minutes=30)
ACTIVE = ("in_progress", "blocked")
IN_SPRINT_OPEN = (
    "assigned",
    "in_progress",
    "blocked",
    "in_review",
    "changes_requested",
)
ITEM_COLUMNS = (
    "id, ref, project, key, title, spec, state, rank, size, lane, assignee, holder, reviewer, "
    "sprint, thread_root, merge_commit, last_status, updated_at"
)


def _cur(conn):
    from psycopg.rows import dict_row

    return conn.cursor(row_factory=dict_row)


def _iso(value):
    if value is None:
        return None
    if isinstance(value, dt.datetime):
        return value.astimezone(dt.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    return value.isoformat()


def _now():
    return dt.datetime.now(dt.UTC)


class Call:
    """One board tool call: the session, the project, the role, the transaction, and the queued mail."""

    def __init__(self, store, ctx, args, tool, write, st):
        if not isinstance(args, dict):
            raise ToolError("INVALID_ARGUMENT")
        self.store, self.ctx, self.args, self.tool, self.write = (
            store,
            ctx,
            args,
            tool,
            write,
        )
        self.st = st
        self.conn = store.open_connection()
        self.cur = _cur(self.conn)
        self.pending = []
        self._project: dict[str, Any] | None = None
        self.role = None
        self.request_id = args.get("request_id") if write else None
        if self.request_id is not None and (
            not isinstance(self.request_id, str) or not 0 < len(self.request_id) <= 200
        ):
            self.close()
            raise ToolError("INVALID_ARGUMENT", path="request_id")
        self.sha = B.content_sha(tool, args) if self.request_id else None

    def close(self):
        try:
            self.conn.close()
        except Exception:  # noqa: BLE001, S110 - a failed mail or close never fails the tool call
            pass

    @property
    def project(self) -> dict[str, Any]:
        """The board.project row; resolve_project sets it before any other step reads it."""
        assert self._project is not None, "resolve_project runs first"
        return self._project

    @project.setter
    def project(self, row: dict[str, Any]) -> None:
        self._project = row

    @property
    def is_owner(self):
        return (
            self.st.login == "owner"
            or self.st.agent == "owner"
            or self.st.kind == "owner"
        )

    def resolve_project(self):
        name = self.args.get("project")
        roles = dict(self.st.roles or {})
        if name is None:
            if len(roles) == 1:
                name = next(iter(roles))
            elif self.is_owner:
                self.cur.execute("SELECT name FROM board.project ORDER BY name")
                names = [r["name"] for r in self.cur.fetchall()]
                if len(names) != 1:
                    raise ToolError("UNKNOWN_PROJECT", path="project")
                name = names[0]
            else:
                raise ToolError("UNKNOWN_PROJECT", path="project")
        if not isinstance(name, str):
            raise ToolError("INVALID_ARGUMENT", path="project")
        self.cur.execute("SELECT * FROM board.project WHERE name = %s", (name,))
        row = self.cur.fetchone()
        if row is None:
            raise ToolError("UNKNOWN_PROJECT", path="project")
        self.project = row
        self.role = "owner" if self.is_owner else roles.get(name)
        if self.role is None:
            raise ToolError("FORBIDDEN_ROLE", path="project")
        B.check_role(self.tool, self.role)
        return row

    def replay(self):
        """Return the first result of a retry, or None. Raise RETRY_KEY_REUSED for different content."""
        if not self.request_id:
            return None
        self.cur.execute(
            "SELECT content_sha, result FROM board.item_event WHERE actor = %s AND request_id = %s",
            (self.st.login, self.request_id),
        )
        row = self.cur.fetchone()
        if row is None:
            return None
        if row["content_sha"] != self.sha:
            raise ToolError("RETRY_KEY_REUSED", path="request_id")
        result = dict(row["result"] or {})
        result["duplicate"] = True
        return result

    def take_supervisor(self):
        """The supervisor lease: one live supervisor session for each project."""
        if self.role != "supervisor":
            return
        self.cur.execute(
            "SELECT supervisor_login, supervisor_lease_end FROM board.project WHERE name = %s FOR UPDATE",
            (self.project["name"],),
        )
        row = self.cur.fetchone()
        # resolve_project read this row in the same transaction, and rows are never deleted.
        assert row is not None
        holder, lease_end = row["supervisor_login"], row["supervisor_lease_end"]
        if holder and holder != self.st.login and lease_end and lease_end > _now():
            raise ToolError(
                "SUPERVISOR_TAKEN",
                path="project",
                owner=B.address(holder),
                lease_until=_iso(lease_end),
            )
        self.cur.execute(
            "UPDATE board.project SET supervisor_login = %s, supervisor_lease_end = %s WHERE name = %s",
            (self.st.login, _now() + SUPERVISOR_LEASE, self.project["name"]),
        )

    def item(self, required=True, lock=True):
        ref = self.args.get("item", self.args.get("id"))
        if ref is None:
            if required:
                raise ToolError("INVALID_ARGUMENT", path="item")
            return None
        if not isinstance(ref, str):
            raise ToolError("INVALID_ARGUMENT", path="item")
        column = "ref" if ref.startswith("itm-") else "key"
        sql = f"SELECT {ITEM_COLUMNS} FROM board.item WHERE project = %s AND {column} = %s"
        self.cur.execute(
            sql + (" FOR UPDATE" if lock else ""), (self.project["name"], ref)
        )
        row = self.cur.fetchone()
        if row is None:
            raise ToolError("UNKNOWN_ITEM", path="item")
        return row

    def event(self, item, from_state, to_state, payload, result):
        self.cur.execute(
            "INSERT INTO board.item_event (item, project, actor, session, role, tool, from_state, to_state, payload,"
            " request_id, content_sha, result) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)",
            (
                item["id"] if item else None,
                self.project["name"],
                self.st.login,
                self.st.handle[:12] if self.st.handle else None,
                self.role,
                self.tool,
                from_state,
                to_state,
                json.dumps(payload, default=str),
                self.request_id,
                self.sha,
                json.dumps(result, default=str),
            ),
        )

    def move(self, item, to_state, payload=None, sets=None):
        """Change the state of an item and add its history row in the same transaction."""
        fields = dict(sets or {})
        fields["state"] = to_state
        assign = ", ".join(f"{k} = %s" for k in fields) + ", updated_at = now()"
        self.cur.execute(
            f"UPDATE board.item SET {assign} WHERE id = %s",
            (*fields.values(), item["id"]),
        )
        return payload or {}

    def supervisor_target(self):
        """The supervisor session that holds the lease, else the supervisor agent address."""
        p = self.project
        if (
            p.get("supervisor_login")
            and p.get("supervisor_lease_end")
            and p["supervisor_lease_end"] > _now()
        ):
            return [p["supervisor_login"]]
        self.cur.execute(
            "SELECT agent FROM mail_auth.membership WHERE project = %s AND role = 'supervisor'",
            (p["name"],),
        )
        return [r["agent"] for r in self.cur.fetchall()] or ["owner"]

    def item_targets(self, item):
        if item.get("holder"):
            return [item["holder"]]
        if item.get("assignee"):
            return [item["assignee"]]
        return self.lane_agent(item.get("lane")) or self.supervisor_target()

    def lane_agent(self, lane):
        if not lane:
            return []
        self.cur.execute(
            "SELECT agent FROM board.lane WHERE project = %s AND name = %s",
            (self.project["name"], lane),
        )
        row = self.cur.fetchone()
        return [row["agent"]] if row else []

    def notice(self, item, to, from_state, to_state, extra=None, kind="notice"):
        body = B.transition_text(
            item["ref"],
            item["title"],
            from_state,
            to_state,
            B.address(self.st.login),
            extra,
        )
        _, raw = B.board_mail(
            to,
            f"[{item['key']}] {to_state}",
            body,
            kind=kind,
            item_ref=item["ref"],
            project=self.project["name"],
            in_reply_to=item["thread_root"],
            references=[item["thread_root"]],
        )
        self.pending.append({"raw": raw, "to": to})

    def session_mail(self, item, kind, subject, body):
        """A mail from the session itself to the supervisor, for status and review requests."""
        _, raw = B.board_mail(
            self.supervisor_target(),
            subject,
            body,
            kind=kind,
            item_ref=item["ref"] if item else None,
            project=self.project["name"],
            in_reply_to=item["thread_root"] if item else None,
            references=[item["thread_root"]] if item else None,
        )
        raw = raw.replace(
            b"From: " + B.address(B.BOARD_LOGIN).encode(),
            b"From: " + B.address(self.st.login).encode(),
            1,
        )
        self.pending.append(
            {"raw": raw, "to": self.supervisor_target(), "from_session": self.st}
        )


def _run(tool, write, body):
    """Wrap a tool body: the session, the project, retries, commit, and board mail after commit."""

    def handler(store, ctx, args, st):
        try:
            return _attempt(store, ctx, args, st)
        except B.BoardError as exc:
            raise ToolError(exc.code, path=exc.path, **exc.extra) from None

    def _attempt(store, ctx, args, st):
        import psycopg

        call = Call(store, ctx, args or {}, tool, write, st)
        try:
            call.resolve_project()
            first = call.replay()
            if first is not None:
                call.conn.rollback()
                return first
            result = body(call)
            call.conn.commit()
        except (ToolError, B.BoardError):
            call.conn.rollback()
            call.close()
            raise
        except psycopg.errors.UniqueViolation as exc:
            call.conn.rollback()
            call.close()
            if "request_id" in str(exc):
                return handler(store, ctx, args, st)
            raise ToolError("INVALID_ARGUMENT", path="key") from exc
        except psycopg.OperationalError as exc:
            call.close()
            raise ToolError("STORE_UNAVAILABLE") from exc
        except Exception as exc:
            call.close()
            log.exception("board tool failed")
            raise ToolError("INTERNAL") from exc
        call.close()
        if write:
            result["notice_sent"] = (
                store.deliver(call.pending) if call.pending else True
            )
            result.setdefault("duplicate", False)
        return result

    handler.__name__ = tool
    return handler


# ---- views -----------------------------------------------------------------------------------


def _item_view(item):
    return {
        "item": item["ref"],
        "key": item["key"],
        "title": item["title"],
        "state": item["state"],
        "rank": float(item["rank"]),
        "size": item["size"],
        "lane": item["lane"],
        "assignee": B.address(item["assignee"]) if item["assignee"] else None,
        "holder": B.address(item["holder"]) if item["holder"] else None,
        "reviewer": B.address(item["reviewer"]) if item["reviewer"] else None,
        "sprint": item["sprint"],
        "thread_ref": B.thread_ref_of(item["thread_root"]),
        "merge_commit": item["merge_commit"],
        "updated_at": _iso(item["updated_at"]),
    }


def _current_sprint(call):
    call.cur.execute(
        "SELECT * FROM board.sprint WHERE project = %s AND closed_at IS NULL ORDER BY id DESC LIMIT 1",
        (call.project["name"],),
    )
    return call.cur.fetchone()


def _sprint_view(s):
    if not s:
        return None
    return {
        "id": s["id"],
        "goal": s["goal"],
        "starts_on": _iso(s["starts_on"]),
        "ends_on": _iso(s["ends_on"]),
        "capacity": s["capacity"],
        "started_at": _iso(s["started_at"]),
        "closed_at": _iso(s["closed_at"]),
        "retro": s["retro"],
    }


def _approvals(call, kind=None, subject=None):
    sql = "SELECT id, subject_kind, subject_id, decision, note, at FROM board.approval WHERE project = %s"
    params = [call.project["name"]]
    if kind:
        sql += " AND subject_kind = %s AND subject_id = %s"
        params += [kind, subject]
    call.cur.execute(sql + " ORDER BY id DESC LIMIT 50", params)
    return [
        {
            "id": r["id"],
            "subject_kind": r["subject_kind"],
            "subject_id": r["subject_id"],
            "decision": r["decision"],
            "note": r["note"],
            "at": _iso(r["at"]),
        }
        for r in call.cur.fetchall()
    ]


# ---- work tools ------------------------------------------------------------------------------


def _get_board(call):
    p = call.project
    call.cur.execute(
        f"SELECT {ITEM_COLUMNS} FROM board.item WHERE project = %s AND state <> 'cancelled'"
        " ORDER BY rank, id LIMIT 200",
        (p["name"],),
    )
    items = call.cur.fetchall()
    call.cur.execute(
        "SELECT name, agent FROM board.lane WHERE project = %s ORDER BY name",
        (p["name"],),
    )
    lanes = []
    for lane in call.cur.fetchall():
        call.cur.execute(
            "SELECT login, last_login_at FROM mail_auth.principal WHERE agent = %s AND kind = 'session'"
            " AND status IN ('issued','active') ORDER BY login",
            (lane["agent"],),
        )
        sessions = [
            {"address": B.address(r["login"]), "last_seen": _iso(r["last_login_at"])}
            for r in call.cur.fetchall()
        ]
        current = next(
            (i for i in items if i["lane"] == lane["name"] and i["state"] in ACTIVE),
            None,
        )
        lanes.append(
            {
                "lane": lane["name"],
                "agent": B.address(lane["agent"]),
                "sessions": sessions,
                "current_item": current["ref"] if current else None,
                "last_status": current["last_status"] if current else None,
            }
        )
    now = _now()
    signals = []
    for i in items:
        if i["state"] == "blocked":
            signals.append({"signal": "item_blocked", "item": i["ref"]})
        if i["state"] == "in_review" and now - i["updated_at"] > dt.timedelta(
            minutes=60
        ):
            signals.append({"signal": "review_waiting", "item": i["ref"]})
    supervisor = None
    if (
        p["supervisor_login"]
        and p["supervisor_lease_end"]
        and p["supervisor_lease_end"] > now
    ):
        supervisor = {
            "address": B.address(p["supervisor_login"]),
            "lease_until": _iso(p["supervisor_lease_end"]),
        }
    return {
        "project": p["name"],
        "goal": p["goal"],
        "role": call.role,
        "wip_limit": p["wip_limit"],
        "supervisor": supervisor,
        "sprint": _sprint_view(_current_sprint(call)),
        "lanes": lanes,
        "items": [_item_view(i) for i in items],
        "signals": signals,
        "approvals": _approvals(call),
    }


def _read_item(call):
    item = call.item(lock=False)
    view = _item_view(item)
    view["spec"] = item["spec"]
    view["last_status"] = item["last_status"]
    call.cur.execute(
        "SELECT d.ref, d.state FROM board.item_dependency x JOIN board.item d ON d.id = x.depends_on"
        " WHERE x.item = %s ORDER BY d.ref",
        (item["id"],),
    )
    view["depends_on"] = [
        {"item": r["ref"], "state": r["state"]} for r in call.cur.fetchall()
    ]
    call.cur.execute(
        "SELECT seq, at, actor, role, tool, from_state, to_state FROM board.item_event WHERE item = %s"
        " ORDER BY seq DESC LIMIT 50",
        (item["id"],),
    )
    view["history"] = [
        {
            "at": _iso(r["at"]),
            "actor": B.address(r["actor"]),
            "role": r["role"],
            "tool": r["tool"],
            "from_state": r["from_state"],
            "to_state": r["to_state"],
        }
        for r in call.cur.fetchall()
    ]
    call.cur.execute(
        "SELECT id, actor, branch, commit_sha, body, at FROM board.evidence WHERE item = %s"
        " ORDER BY id DESC LIMIT 1",
        (item["id"],),
    )
    ev = call.cur.fetchone()
    view["evidence"] = (
        {
            "evidence": f"evd-{ev['id']}",
            "actor": B.address(ev["actor"]),
            "branch": ev["branch"],
            "commit": ev["commit_sha"],
            "checks": ev["body"].get("checks", []),
            "at": _iso(ev["at"]),
        }
        if ev
        else None
    )
    view["approvals"] = _approvals(call, "item", item["id"])
    return view


def _start_item(call):
    st, p = call.st, call.project
    call.cur.execute(
        "SELECT count(*) AS n FROM board.item WHERE holder = %s AND state IN ('in_progress','blocked')",
        (st.login,),
    )
    B.check_wip(call.cur.fetchone()["n"], p["wip_limit"])
    if call.args.get("item", call.args.get("id")) is not None:
        item = call.item()
        if call.role == "worker" and item["assignee"] != st.agent:
            raise ToolError("FORBIDDEN_ROLE", path="item")
        if (
            item["state"] == "changes_requested"
            and item["holder"] not in (None, st.login)
            and call.role == "worker"
        ):
            raise ToolError(
                "THREAD_CLAIMED", path="item", owner=B.address(item["holder"])
            )
    else:
        call.cur.execute(
            f"SELECT {ITEM_COLUMNS} FROM board.item WHERE project = %s AND assignee = %s AND"
            " (state = 'assigned' OR (state = 'changes_requested' AND (holder IS NULL OR holder = %s)))"
            " ORDER BY rank, id LIMIT 1 FOR UPDATE SKIP LOCKED",
            (p["name"], st.agent, st.login),
        )
        item = call.cur.fetchone()
        if item is None:
            result = {"item": None, "thread_ref": None, "state": None}
            call.event(None, None, None, {}, result)
            return result
    B.check_transition(item["state"], "in_progress", "start_item")
    claim = call.store.claim_thread(st, B.thread_ref_of(item["thread_root"]))
    from_state = item["state"]
    call.move(item, "in_progress", sets={"holder": st.login})
    item = dict(item, state="in_progress", holder=st.login)
    result = {
        "item": item["ref"],
        "key": item["key"],
        "title": item["title"],
        "state": "in_progress",
        "thread_ref": B.thread_ref_of(item["thread_root"]),
        "lease_until": claim.get("lease_until"),
        "moved": claim.get("moved", []),
    }
    call.event(item, from_state, "in_progress", {}, result)
    call.notice(item, call.supervisor_target(), from_state, "in_progress")
    return result


def _report_status(call):
    st = call.st
    text = call.args.get("text", call.args.get("summary", ""))
    if not isinstance(text, str) or len(text) > 4000:
        raise ToolError("INVALID_ARGUMENT", path="text")
    target = call.args.get("state")
    item = call.item(required=False)
    status = {"text": text, "at": _iso(_now()), "by": B.address(st.login)}
    if item is None:
        if target is not None:
            raise ToolError("INVALID_ARGUMENT", path="item")
        result = {"item": None, "state": None, "recorded": True}
        call.event(None, None, None, status, result)
        _, raw = B.board_mail(
            call.supervisor_target(),
            "status",
            text + "\n",
            kind="status",
            project=call.project["name"],
        )
        raw = raw.replace(
            b"From: " + B.address(B.BOARD_LOGIN).encode(),
            b"From: " + B.address(st.login).encode(),
            1,
        )
        call.pending.append(
            {"raw": raw, "to": call.supervisor_target(), "from_session": st}
        )
        return result
    if item["holder"] != st.login and call.role not in ("supervisor", "owner"):
        raise ToolError("FORBIDDEN_ROLE", path="item")
    from_state = item["state"]
    if target is not None and target != from_state:
        B.check_transition(from_state, target, "report_status")
        if target == "blocked":
            cause = call.args.get("cause")
            if not isinstance(cause, str) or not cause.strip():
                raise ToolError("INVALID_ARGUMENT", path="cause")
            status["cause"] = cause
        call.move(item, target, sets={"last_status": json.dumps(status)})
    else:
        target = from_state
        call.cur.execute(
            "UPDATE board.item SET last_status = %s, updated_at = now() WHERE id = %s",
            (json.dumps(status), item["id"]),
        )
    result = {"item": item["ref"], "state": target, "recorded": True}
    call.event(item, from_state, target, status, result)
    body = (
        B.transition_text(
            item["ref"],
            item["title"],
            from_state,
            target,
            B.address(st.login),
            {"cause": status.get("cause")},
        )
        + "\n"
        + text
        + "\n"
    )
    call.session_mail(item, "status", f"[{item['key']}] status: {target}", body)
    return result


def _submit_for_review(call):
    st = call.st
    item = call.item()
    if item["holder"] != st.login and call.role != "owner":
        raise ToolError("FORBIDDEN_ROLE", path="item")
    B.check_transition(item["state"], "in_review", "submit_for_review")
    evidence = B.check_evidence(call.project["done_rule"], call.args.get("evidence"))
    call.cur.execute(
        "INSERT INTO board.evidence (item, actor, branch, commit_sha, body) VALUES (%s,%s,%s,%s,%s)"
        " RETURNING id",
        (
            item["id"],
            st.login,
            evidence["branch"],
            evidence["commit"],
            json.dumps(evidence),
        ),
    )
    ev_ref = f"evd-{call.cur.fetchone()['id']}"
    call.move(item, "in_review")
    result = {"item": item["ref"], "state": "in_review", "evidence": ev_ref}
    call.event(
        item,
        item["state"],
        "in_review",
        {"evidence": ev_ref, "commit": evidence["commit"]},
        result,
    )
    body = B.transition_text(
        item["ref"],
        item["title"],
        item["state"],
        "in_review",
        B.address(st.login),
        {
            "evidence": ev_ref,
            "branch": evidence["branch"],
            "commit": evidence["commit"],
        },
    )
    call.session_mail(item, "request", f"[{item['key']}] review requested", body)
    return result


# ---- supervision tools -----------------------------------------------------------------------


def _str_list(value, path):
    if value is None:
        return []
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        raise ToolError("INVALID_ARGUMENT", path=path)
    return value


def _new_item(call, key, title, spec, lane=None, rank=None, size=None, reviewer=None):
    p = call.project
    if rank is None:
        call.cur.execute(
            "SELECT coalesce(max(rank), 0) + 1 AS r FROM board.item WHERE project = %s",
            (p["name"],),
        )
        rank = call.cur.fetchone()["r"]
    root, ref = B.new_message_id(), B.new_item_ref()
    call.cur.execute(
        f"INSERT INTO board.item (ref, project, key, title, spec, state, rank, size, lane, reviewer, thread_root)"
        f" VALUES (%s,%s,%s,%s,%s,'backlog',%s,%s,%s,%s,%s) RETURNING {ITEM_COLUMNS}",
        (
            ref,
            p["name"],
            key,
            title,
            json.dumps(spec),
            rank,
            size,
            lane,
            reviewer,
            root,
        ),
    )
    item = call.cur.fetchone()
    to = call.lane_agent(lane) or call.supervisor_target()
    body = B.transition_text(
        ref,
        title,
        None,
        "backlog",
        B.address(call.st.login),
        {
            "goal": spec.get("goal"),
            "acceptance criteria": spec.get("acceptance_criteria"),
        },
    )
    _, raw = B.board_mail(
        to,
        f"[{key}] {title}",
        body,
        kind="notice",
        item_ref=ref,
        project=p["name"],
        message_id=root,
    )
    call.pending.append({"raw": raw, "to": to})
    return item


def _plan_item(call):
    call.take_supervisor()
    a = call.args
    spec = a.get("spec")
    if spec is not None and not isinstance(spec, dict):
        raise ToolError("INVALID_ARGUMENT", path="spec")
    target = a.get("state", "ready")
    if target not in B.STATES:
        raise ToolError("INVALID_ARGUMENT", path="state")
    lane, size, rank, reviewer = (
        a.get("lane"),
        a.get("size"),
        a.get("rank"),
        a.get("reviewer"),
    )
    if rank is not None and not isinstance(rank, (int, float)):
        raise ToolError("INVALID_ARGUMENT", path="rank")
    if lane is not None and not call.lane_agent(lane):
        raise ToolError("INVALID_ARGUMENT", path="lane")
    reviewer = reviewer.split("@")[0] if isinstance(reviewer, str) else None
    if a.get("item", a.get("id")) is None:
        key, title = a.get("key"), a.get("title")
        if not isinstance(key, str) or not key.strip() or len(key) > 64:
            raise ToolError("INVALID_ARGUMENT", path="key")
        if not isinstance(title, str) or not title.strip() or len(title) > 200:
            raise ToolError("INVALID_ARGUMENT", path="title")
        item = _new_item(
            call, key, " ".join(title.split()), spec or {}, lane, rank, size, reviewer
        )
        created = True
    else:
        item = call.item()
        created = False
        sets = {
            k: v
            for k, v in (
                ("title", a.get("title")),
                ("lane", lane),
                ("size", size),
                ("rank", rank),
                ("reviewer", reviewer),
            )
            if v is not None
        }
        if spec is not None:
            sets["spec"] = json.dumps(dict(item["spec"], **spec))
        if sets:
            call.cur.execute(
                f"UPDATE board.item SET {', '.join(k + ' = %s' for k in sets)}, updated_at = now()"
                f" WHERE id = %s RETURNING {ITEM_COLUMNS}",
                (*sets.values(), item["id"]),
            )
            item = call.cur.fetchone()
    deps = _str_list(a.get("depends_on"), "depends_on")
    for dep in deps:
        call.cur.execute(
            "SELECT id FROM board.item WHERE project = %s AND ref = %s",
            (call.project["name"], dep),
        )
        row = call.cur.fetchone()
        if row is None or row["id"] == item["id"]:
            raise ToolError("UNKNOWN_ITEM", path="depends_on")
        call.cur.execute(
            "INSERT INTO board.item_dependency (item, depends_on) VALUES (%s,%s) ON CONFLICT DO NOTHING",
            (item["id"], row["id"]),
        )
    from_state = item["state"]
    if target != from_state:
        B.check_transition(from_state, target, "plan_item")
        if target == "ready":
            B.check_plan(item["spec"])
        sets = (
            {"holder": None, "assignee": None} if target in ("backlog", "ready") else {}
        )
        call.move(item, target, sets=sets)
        if not created:
            call.notice(item, call.item_targets(item), from_state, target)
    result = {
        "item": item["ref"],
        "key": item["key"],
        "state": target,
        "created": created,
        "thread_ref": B.thread_ref_of(item["thread_root"]),
    }
    call.event(
        item, None if created else from_state, target, {"depends_on": deps}, result
    )
    return result


def _assign_item(call):
    call.take_supervisor()
    item = call.item()
    B.check_transition(item["state"], "assigned", "assign_item")
    lane = call.args.get("lane", item["lane"])
    assignee = call.args.get("assignee")
    lane_agent = call.lane_agent(lane)
    if lane and not lane_agent:
        raise ToolError("INVALID_ARGUMENT", path="lane")
    if assignee is None:
        if not lane_agent:
            raise ToolError("INVALID_ARGUMENT", path="assignee")
        assignee = lane_agent[0]
    if not isinstance(assignee, str):
        raise ToolError("INVALID_ARGUMENT", path="assignee")
    assignee = assignee.split("@")[0]
    call.cur.execute(
        "SELECT role FROM mail_auth.membership WHERE project = %s AND agent = %s",
        (call.project["name"], assignee),
    )
    if call.cur.fetchone() is None:
        raise ToolError("RECIPIENT_UNKNOWN", path="assignee")
    call.cur.execute(
        "SELECT d.ref, d.state FROM board.item_dependency x JOIN board.item d ON d.id = x.depends_on"
        " WHERE x.item = %s",
        (item["id"],),
    )
    B.check_dependencies({r["ref"]: r["state"] for r in call.cur.fetchall()})
    sprint = call.args.get("sprint")
    if sprint is None:
        current = _current_sprint(call)
        sprint = current["id"] if current else None
        capacity = current["capacity"] if current else {}
    else:
        call.cur.execute(
            "SELECT * FROM board.sprint WHERE id = %s AND project = %s AND closed_at IS NULL",
            (sprint, call.project["name"]),
        )
        row = call.cur.fetchone()
        if row is None:
            raise ToolError("INVALID_ARGUMENT", path="sprint")
        capacity = row["capacity"]
    if sprint is not None and lane:
        call.cur.execute(
            "SELECT count(*) AS n FROM board.item WHERE sprint = %s AND lane = %s AND state = ANY(%s)",
            (sprint, lane, list(IN_SPRINT_OPEN)),
        )
        B.check_capacity(lane, call.cur.fetchone()["n"], capacity)
    call.move(
        item,
        "assigned",
        sets={"assignee": assignee, "lane": lane, "sprint": sprint, "holder": None},
    )
    item = dict(item, assignee=assignee, lane=lane, sprint=sprint, holder=None)
    result = {
        "item": item["ref"],
        "state": "assigned",
        "assignee": B.address(assignee),
        "lane": lane,
        "sprint": sprint,
    }
    call.event(item, "ready", "assigned", {"assignee": assignee}, result)
    call.notice(item, [assignee], "ready", "assigned")
    return result


def _review_item(call):
    item = call.item()
    if call.role == "reviewer" and item["reviewer"] != call.st.agent:
        raise ToolError("FORBIDDEN_ROLE", path="item")
    call.take_supervisor()
    target = B.check_review(call.args.get("decision"), call.args)
    B.check_transition(item["state"], target, "review_item")
    findings = [
        f for f in _str_list(call.args.get("findings"), "findings") if f.strip()
    ]
    sets = (
        {"merge_commit": call.args["merge_commit"].lower()} if target == "done" else {}
    )
    call.move(item, target, sets=sets)
    result = {
        "item": item["ref"],
        "state": target,
        "merge_commit": sets.get("merge_commit"),
    }
    call.event(item, "in_review", target, {"findings": findings, **sets}, result)
    call.notice(
        item,
        call.item_targets(item),
        "in_review",
        target,
        {"findings": findings or None, "merge commit": sets.get("merge_commit")},
    )
    return result


def _manage_sprint(call):
    call.take_supervisor()
    a, p = call.args, call.project
    action = a.get("action")
    if action == "create":
        goal, starts, ends = a.get("goal"), a.get("starts_on"), a.get("ends_on")
        if not isinstance(goal, str) or not goal.strip():
            raise ToolError("INVALID_ARGUMENT", path="goal")
        try:
            starts, ends = dt.date.fromisoformat(starts), dt.date.fromisoformat(ends)
        except (TypeError, ValueError):
            raise ToolError("INVALID_ARGUMENT", path="starts_on") from None
        if ends < starts:
            raise ToolError("INVALID_ARGUMENT", path="ends_on")
        capacity = a.get("capacity") or {}
        if not isinstance(capacity, dict) or not all(
            isinstance(v, int) and v >= 0 for v in capacity.values()
        ):
            raise ToolError("INVALID_ARGUMENT", path="capacity")
        if _current_sprint(call) is not None:
            raise ToolError("INVALID_TRANSITION", path="action")
        call.cur.execute(
            "INSERT INTO board.sprint (project, goal, starts_on, ends_on, capacity, started_at)"
            " VALUES (%s,%s,%s,%s,%s,now()) RETURNING *",
            (p["name"], goal, starts, ends, json.dumps(capacity)),
        )
        sprint = call.cur.fetchone()
        result = {"sprint": _sprint_view(sprint), "created_items": []}
    elif action in ("update", "close"):
        sprint = _current_sprint(call)
        if sprint is None or (
            a.get("sprint") is not None and a.get("sprint") != sprint["id"]
        ):
            raise ToolError("INVALID_TRANSITION", path="sprint")
        created = []
        if action == "update":
            sets = {}
            if isinstance(a.get("goal"), str) and a["goal"].strip():
                sets["goal"] = a["goal"]
            if a.get("capacity") is not None:
                if not isinstance(a["capacity"], dict):
                    raise ToolError("INVALID_ARGUMENT", path="capacity")
                sets["capacity"] = json.dumps(a["capacity"])
            if not sets:
                raise ToolError("INVALID_ARGUMENT", path="action")
        else:
            retro = a.get("retro")
            if retro is not None and not isinstance(retro, str):
                raise ToolError("INVALID_ARGUMENT", path="retro")
            sets = {"retro": retro, "closed_at": _now()}
            for n, action_text in enumerate(
                _str_list(a.get("retro_actions"), "retro_actions"), 1
            ):
                text = " ".join(action_text.split())[:200]
                if not text:
                    continue
                item = _new_item(
                    call,
                    f"retro-{sprint['id']}-{n}",
                    text,
                    {
                        "goal": text,
                        "acceptance_criteria": [],
                        "source": f"retro of sprint {sprint['id']}",
                    },
                )
                created.append(item["ref"])
            # Open items that did not finish go back to the backlog of the next sprint.
            call.cur.execute(
                "UPDATE board.item SET sprint = NULL WHERE sprint = %s AND state NOT IN ('done','cancelled')",
                (sprint["id"],),
            )
        call.cur.execute(
            f"UPDATE board.sprint SET {', '.join(k + ' = %s' for k in sets)} WHERE id = %s RETURNING *",
            (*sets.values(), sprint["id"]),
        )
        sprint = call.cur.fetchone()
        result = {"sprint": _sprint_view(sprint), "created_items": created}
    else:
        raise ToolError("INVALID_ARGUMENT", path="action")
    call.event(None, None, None, {"action": action, "sprint": sprint["id"]}, result)
    return result


TOOLS = {
    "get_board": _run("get_board", False, _get_board),
    "read_item": _run("read_item", False, _read_item),
    "start_item": _run("start_item", True, _start_item),
    "report_status": _run("report_status", True, _report_status),
    "submit_for_review": _run("submit_for_review", True, _submit_for_review),
    "plan_item": _run("plan_item", True, _plan_item),
    "assign_item": _run("assign_item", True, _assign_item),
    "review_item": _run("review_item", True, _review_item),
    "manage_sprint": _run("manage_sprint", True, _manage_sprint),
}


class PostgresBoardStore(BoardStore):
    """The board store on PostgreSQL. connect opens a sync psycopg connection with autocommit off."""

    def __init__(self, connect, mailer, claims):
        self._connect = connect
        self._mailer = mailer
        self._claims = claims

    def open_connection(self):
        try:
            return self._connect()
        except ToolError:
            raise
        except Exception as exc:
            raise ToolError("STORE_UNAVAILABLE") from exc

    def claim_thread(self, state, thread_ref):
        """Claim the thread of an item through the threads module. Runs in the worker thread."""
        claim = anyio.from_thread.run(
            partial(self._claims.claim, state, thread_ref=thread_ref)
        )
        return claim if isinstance(claim, dict) else {}

    def deliver(self, pending):
        """Send the mails that a committed transaction queued. Return True when each one went."""
        ok = True
        for entry in pending:
            st = entry.get("from_session")
            if st is not None and getattr(st, "mail", None) is not None:
                try:
                    st.mail.send(
                        entry["raw"],
                        B.address(st.login),
                        [B.address(r) for r in entry["to"]],
                    )
                except Exception:  # noqa: BLE001 - a failed mail or close never fails the tool call
                    log.warning("status mail not sent")
                    ok = False
            else:
                ok = self._mailer.send(entry["raw"], entry["to"]) and ok
        return ok

    async def call(self, tool, ctx, args, state):
        handler = TOOLS[tool]
        return await anyio.to_thread.run_sync(partial(handler, self, ctx, args, state))

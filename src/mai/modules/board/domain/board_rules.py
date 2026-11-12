"""Pure rules of the Scrum board: states, transitions, guards, the evidence rule, refs, and board mail.

Nothing here touches the database or the mail server, thus it is unit-testable on the host.
"""

import base64
import hashlib
import json
import secrets
import uuid
from email.message import EmailMessage
from email.utils import formatdate


class BoardError(Exception):
    """A broken board rule. code is a tool error code of the MCP contract; the tool layer changes it into a ToolError."""

    def __init__(self, code, path=None, **extra):
        super().__init__(code)
        self.code = code
        self.path = path
        self.extra = extra


DOMAIN = "mai.internal"
BOARD_LOGIN = "board"

STATES = (
    "backlog",
    "ready",
    "assigned",
    "in_progress",
    "blocked",
    "in_review",
    "changes_requested",
    "done",
    "cancelled",
)
ROLES = ("owner", "supervisor", "worker", "reviewer")
OPEN_STATES = frozenset(STATES) - {"done", "cancelled"}

# (from, to) -> (tool, roles that may do it). "self" means the worker session that holds the item.
TRANSITIONS = {
    ("backlog", "ready"): ("plan_item", {"supervisor"}),
    ("ready", "assigned"): ("assign_item", {"supervisor"}),
    ("assigned", "in_progress"): ("start_item", {"worker", "supervisor"}),
    ("changes_requested", "in_progress"): ("start_item", {"worker", "supervisor"}),
    ("in_progress", "blocked"): ("report_status", {"self"}),
    ("blocked", "in_progress"): ("report_status", {"self"}),
    ("in_progress", "in_review"): ("submit_for_review", {"self"}),
    ("in_review", "done"): ("review_item", {"supervisor", "reviewer"}),
    ("in_review", "changes_requested"): ("review_item", {"supervisor", "reviewer"}),
}
# The supervisor can move an open item back to ready or backlog, or cancel it.
SUPERVISOR_MOVES = ("ready", "backlog", "cancelled")

# Tool -> roles of the caller that may call it. The owner may call each tool.
TOOL_ROLES = {
    "get_board": {"worker", "supervisor", "reviewer"},
    "read_item": {"worker", "supervisor", "reviewer"},
    "start_item": {"worker", "supervisor"},
    "report_status": {"worker", "supervisor", "reviewer"},
    "submit_for_review": {"worker", "supervisor"},
    "plan_item": {"supervisor"},
    "assign_item": {"supervisor"},
    "review_item": {"supervisor", "reviewer"},
    "manage_sprint": {"supervisor"},
}
SUPERVISOR_TOOLS = {"plan_item", "assign_item", "manage_sprint"}

SHA40 = frozenset("0123456789abcdef")


def check_transition(from_state, to_state, tool):
    """Return the roles that may do this transition with this tool, or raise INVALID_TRANSITION."""
    if from_state == to_state and tool != "plan_item":
        raise BoardError("INVALID_TRANSITION", path="state", state=from_state)
    entry = TRANSITIONS.get((from_state, to_state))
    if entry and entry[0] == tool:
        return entry[1]
    if (
        tool == "plan_item"
        and to_state in SUPERVISOR_MOVES
        and from_state in OPEN_STATES
    ):
        return {"supervisor"}
    raise BoardError("INVALID_TRANSITION", path="state", state=from_state)


def check_role(tool, role):
    """Raise FORBIDDEN_ROLE when the project role does not permit the tool."""
    if role == "owner":
        return
    if role not in TOOL_ROLES.get(tool, set()):
        raise BoardError("FORBIDDEN_ROLE", path=tool)


def check_plan(spec):
    """The guard of backlog -> ready: a goal and at least one acceptance criterion."""
    if not (spec or {}).get("goal"):
        raise BoardError("INVALID_TRANSITION", path="spec.goal")
    criteria = (spec or {}).get("acceptance_criteria") or []
    if not [c for c in criteria if str(c).strip()]:
        raise BoardError("INVALID_TRANSITION", path="spec.acceptance_criteria")


def check_dependencies(dependency_states):
    """dependency_states maps an item ref to its state. Raise DEPENDENCY_OPEN with the open refs."""
    open_refs = sorted(
        ref for ref, state in dependency_states.items() if state != "done"
    )
    if open_refs:
        raise BoardError("DEPENDENCY_OPEN", path="depends_on", open=open_refs)


def check_capacity(lane, in_lane_count, capacity):
    """capacity maps a lane to its maximum number of open sprint items. A lane with no entry has no limit."""
    limit = (capacity or {}).get(lane) if lane else None
    if limit is not None and in_lane_count >= int(limit):
        raise BoardError("WIP_LIMIT", path="lane", lane=lane, limit=int(limit))


def check_wip(active_count, wip_limit):
    """The work-in-progress limit of one session."""
    if active_count >= wip_limit:
        raise BoardError("WIP_LIMIT", path="session", limit=wip_limit)


def check_evidence(done_rule, evidence):
    """Return the normalized evidence, or raise EVIDENCE_INCOMPLETE naming each missing or failed check."""
    evidence = evidence or {}
    if not isinstance(evidence, dict):
        raise BoardError("INVALID_ARGUMENT", path="evidence")
    commit = str(evidence.get("commit") or "").lower()
    if len(commit) != 40 or not set(commit) <= SHA40:
        raise BoardError("INVALID_ARGUMENT", path="evidence.commit")
    if not evidence.get("branch"):
        raise BoardError("INVALID_ARGUMENT", path="evidence.branch")
    given = {}
    for entry in evidence.get("checks") or []:
        if not isinstance(entry, dict) or not entry.get("name"):
            raise BoardError("INVALID_ARGUMENT", path="evidence.checks")
        if entry.get("result") not in ("pass", "fail"):
            raise BoardError("INVALID_ARGUMENT", path="evidence.checks.result")
        given[entry["name"]] = entry
    missing, failed = [], []
    for check in (done_rule or {}).get("checks", []):
        if not check.get("required"):
            continue
        entry = given.get(check["name"])
        if entry is None:
            missing.append(check["name"])
        elif entry["result"] != "pass":
            failed.append(check["name"])
        elif "min_coverage" in check:
            coverage = entry.get("coverage")
            if coverage is None or float(coverage) < float(check["min_coverage"]):
                failed.append(check["name"])
    if missing or failed:
        raise BoardError(
            "EVIDENCE_INCOMPLETE",
            path="evidence.checks",
            missing=missing,
            failed=failed,
        )
    out = dict(evidence)
    out["commit"] = commit
    for key in ("decisions", "deviations", "questions"):
        out[key] = [str(v) for v in out.get(key) or []]
    return out


def check_review(decision, args):
    """Return the target state of a review, or raise for a missing merge commit or missing findings."""
    if decision == "accept":
        commit = str(args.get("merge_commit") or "").lower()
        if len(commit) != 40 or not set(commit) <= SHA40:
            raise BoardError("INVALID_ARGUMENT", path="merge_commit")
        return "done"
    if decision == "changes":
        findings = [f for f in args.get("findings") or [] if str(f).strip()]
        if not findings:
            raise BoardError("INVALID_ARGUMENT", path="findings")
        return "changes_requested"
    raise BoardError("INVALID_ARGUMENT", path="decision")


def new_item_ref():
    """itm- and 12 characters of lower-case base32."""
    return (
        "itm-" + base64.b32encode(secrets.token_bytes(10)).decode("ascii").lower()[:12]
    )


def new_message_id():
    return f"<msg-{uuid.uuid4()}@{DOMAIN}>"


def thread_ref_of(message_id):
    """thr- and the first 16 hex digits of the SHA-256 of the root Message-ID."""
    return "thr-" + hashlib.sha256((message_id or "").encode("utf-8")).hexdigest()[:16]


def content_sha(tool, args):
    """The retry fingerprint of a write call: the tool and its arguments without session and request_id."""
    clean = {
        k: v for k, v in sorted(args.items()) if k not in ("session", "request_id")
    }
    return hashlib.sha256(
        json.dumps([tool, clean], sort_keys=True, default=str).encode("utf-8")
    ).hexdigest()


def address(login):
    login = (login or "").strip()
    return login if "@" in login else f"{login}@{DOMAIN}"


def board_mail(
    to,
    subject,
    body,
    kind="notice",
    item_ref=None,
    project=None,
    message_id=None,
    in_reply_to=None,
    references=None,
):
    """Build a board mail from board@mai.internal. Return (message_id, raw bytes)."""
    message_id = message_id or new_message_id()
    msg = EmailMessage()
    msg["From"] = address(BOARD_LOGIN)
    msg["To"] = ", ".join(address(t) for t in to)
    msg["Subject"] = " ".join(str(subject).split())[:200]
    msg["Date"] = formatdate(usegmt=True)
    msg["Message-ID"] = message_id
    msg["X-Mai-Kind"] = kind
    msg["X-Mai-Priority"] = "normal"
    if item_ref:
        msg["X-Mai-Item"] = item_ref
    if project:
        msg["X-Mai-Project"] = project
    if in_reply_to:
        msg["In-Reply-To"] = in_reply_to
        msg["References"] = " ".join(references or [in_reply_to])
    msg.set_content(body)
    return message_id, msg.as_bytes()


def transition_text(item_ref, title, from_state, to_state, actor, extra=None):
    """The body of a transition notice. It holds only board data, never mail text."""
    lines = [
        f"Item {item_ref}: {title}",
        f"State: {from_state or '-'} -> {to_state}",
        f"By: {actor}",
    ]
    for key, value in (extra or {}).items():
        if isinstance(value, (list, tuple)):
            lines.append(f"{key}:")
            lines.extend(f"- {v}" for v in value)
        elif value is not None:
            lines.append(f"{key}: {value}")
    return "\n".join(lines) + "\n"

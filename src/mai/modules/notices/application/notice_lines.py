"""A notice has only text that the server makes: the ref, the agent of the proved sender, the kind and
the priority. It never has the subject or the body.

The set of known agents is data: the caller gives it (the active agents of the credentials module and
the system accounts).
"""

from __future__ import annotations

from collections.abc import Iterable

from mai.platform.mailserver import (
    authenticated_sender,
    login_of,
    parse_summary,
    sender_proved,
)

NOTIFIED = "$MaiNotified"
SEARCH = f"UNSEEN UNKEYWORD {NOTIFIED} UNKEYWORD $MaiDone"
SYSTEM_ACCOUNTS = frozenset({"owner", "board"})


def known_set(agent_names: Iterable[str]) -> frozenset[str]:
    """Give the agents that a notice can name: the given agents and the system accounts."""
    return frozenset(agent_names) | SYSTEM_ACCOUNTS


def agent_of_login(login: str) -> str:
    """Give the agent of a login: "cursor" for the session "cursor.lane-a"."""
    return login.split(".", 1)[0] if login else ""


def sender_agent(headers: dict, known: frozenset[str]) -> str:
    """Give the agent of the proved sender, or "unknown"."""
    if not sender_proved(headers):
        return "unknown"
    agent = agent_of_login(login_of(authenticated_sender(headers) or ""))
    return agent if agent in known else "unknown"


def notice_line(headers: dict, known: frozenset[str]) -> str | None:
    summary = parse_summary(headers)
    if not summary.get("ref"):
        return None
    return (
        f"mai: new mail {summary['ref']} from {sender_agent(headers, known)}"
        f" kind {summary['kind']} priority {summary['priority']}"
    )


def mailboxes_of(login: str, known: frozenset[str]) -> list[str]:
    """The INBOX, and the agent queue for a session login ("<agent>.<name>")."""
    boxes = ["INBOX"]
    if "." in login:
        agent = agent_of_login(login)
        if agent in known and agent not in SYSTEM_ACCOUNTS:
            boxes.append(f"shared/{agent}/INBOX")
    return boxes

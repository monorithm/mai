"""claim and release of a thread.

A claim moves each mail of a thread from the shared agent queue to the INBOX
of one session and keeps a lease row. The lock of the claim store on (agent,
thread root) makes sure that two sessions cannot claim one thread at the same
time. The mail calls are blocking IMAP calls, thus they run in a worker thread.
"""

from __future__ import annotations

import re

import anyio.to_thread
from bustan import Injectable

from mai.platform.contract import ToolError
from mai.platform.mailserver import parse_summary, ref_of, thread_ref_of

from ..domain.claim import LEASE, ClaimStore, iso, queue_of
from ..domain.returner import ThreadMailReturner

_REF = re.compile(r"^ref-[0-9a-f]{16}$")
_THREAD_REF = re.compile(r"^thr-[0-9a-f]{16}$")


def _summaries(mail, mailbox: str) -> list[dict]:
    try:
        uids = mail.search(mailbox, "ALL")
        if not uids:
            return []
        rows = mail.fetch_headers(mailbox, uids)
    except ToolError:
        raise
    except Exception as exc:
        raise ToolError("MAIL_SERVER_DOWN") from exc
    out = []
    for row in rows:
        summary = parse_summary(row.get("headers") or {})
        summary["uid"] = row.get("uid")
        summary["flags"] = tuple(row.get("flags") or ())
        out.append(summary)
    return out


def _move_thread(
    mail, source: str, dest: str, root: str, only_unread: bool = False
) -> list[str]:
    """Move each mail of the thread from source to dest. Give only the refs of moves with COPYUID."""
    moved = []
    for summary in _summaries(mail, source):
        if summary["root_message_id"] != root:
            continue
        if only_unread and "\\Seen" in summary["flags"]:
            continue
        try:
            if mail.move(source, summary["uid"], dest) and summary["ref"]:
                moved.append(summary["ref"])
        except ToolError:
            raise
        except Exception as exc:
            raise ToolError("MAIL_SERVER_DOWN") from exc
    return moved


def _scan_root(mail, agent: str, ref: str | None, thread_ref: str | None) -> str | None:
    for mailbox in (queue_of(agent), "INBOX", "Done", "Sent"):
        for summary in _summaries(mail, mailbox):
            if ref and summary["ref"] == ref:
                return summary["root_message_id"]
            if thread_ref and summary["thread_ref"] == thread_ref:
                return summary["root_message_id"]
    return None


@Injectable()
class ClaimService:
    def __init__(self, store: ClaimStore, returner: ThreadMailReturner) -> None:
        self._store = store
        self._returner = returner

    async def _call(self, state, fn, *args):
        def run():
            with state.lock:
                return fn(state.mail, *args)

        return await anyio.to_thread.run_sync(run)

    async def thread_root(self, state, ref: str | None, thread_ref: str | None) -> str:
        """Find the root Message-ID of the thread that ref or thread_ref names."""
        if ref is not None and (not isinstance(ref, str) or not _REF.match(ref)):
            raise ToolError("INVALID_ARGUMENT", path="ref")
        if thread_ref is not None and (
            not isinstance(thread_ref, str) or not _THREAD_REF.match(thread_ref)
        ):
            raise ToolError("INVALID_ARGUMENT", path="thread_ref")
        if not ref and not thread_ref:
            raise ToolError("INVALID_ARGUMENT", path="thread_ref")
        known = await self._store.roots_of(state.agent)
        if thread_ref:
            # A known claim of this agent gives the root without a mailbox scan.
            for root in known:
                if thread_ref_of(root) == thread_ref:
                    return root
        root = await self._call(state, _scan_root, state.agent, ref, thread_ref)
        if root:
            return root
        if ref:
            # The root mail can be in the INBOX of the session that holds the claim.
            for root in known:
                if ref_of(root) == ref:
                    return root
            raise ToolError("UNKNOWN_MESSAGE", path="ref")
        raise ToolError("UNKNOWN_MESSAGE", path="thread_ref")

    async def claim(
        self, state, ref: str | None = None, thread_ref: str | None = None
    ) -> dict:
        root = await self.thread_root(state, ref, thread_ref)
        async with self._store.locked(state.agent, root) as txn:
            row = await txn.get()
            if row and row.live(await txn.now()) and row.login != state.login:
                raise ToolError(
                    "THREAD_CLAIMED",
                    path="thread_ref",
                    owner=row.login,
                    lease_until=iso(row.lease_end),
                )
            # The claim of a different session ended: its unread mail of the thread goes back
            # to the queue first, thus this claim gets it. If the return fails for a live old
            # holder, the claim does not go on, else that mail would stay with it. A revoked
            # old holder has no queue rights: its revoke returned its mail before.
            if (
                row
                and row.login != state.login
                and not await anyio.to_thread.run_sync(
                    self._returner, row.login, state.agent, root
                )
                and await self._store.session_live(row.login)
            ):
                raise ToolError("MAIL_SERVER_DOWN", path="thread_ref")
            moved = await self._call(
                state, _move_thread, queue_of(state.agent), "INBOX", root
            )
            claim = await txn.put(state.login, LEASE)
        return {
            "thread_ref": thread_ref_of(root),
            "owner": state.login,
            "lease_until": iso(claim.lease_end),
            "moved": moved,
        }

    async def release(self, state, thread_ref: str | None) -> dict:
        if not thread_ref:
            raise ToolError("INVALID_ARGUMENT", path="thread_ref")
        root = await self.thread_root(state, None, thread_ref)
        async with self._store.locked(state.agent, root) as txn:
            row = await txn.get()
            live = bool(row and row.live(await txn.now()))
            if live and row.login != state.login:
                raise ToolError(
                    "THREAD_CLAIMED",
                    path="thread_ref",
                    owner=row.login,
                    lease_until=iso(row.lease_end),
                )
            if not live:
                return {
                    "thread_ref": thread_ref_of(root),
                    "released": False,
                    "returned": [],
                }
            returned = await self._call(
                state, _move_thread, "INBOX", queue_of(state.agent), root, True
            )
            await txn.end()
        return {
            "thread_ref": thread_ref_of(root),
            "released": True,
            "returned": returned,
        }

    async def holder(self, agent: str, root_msg_id: str) -> str | None:
        """Give the session login that holds a live claim on the thread, or None."""
        return await self._store.live_holder(agent, root_msg_id)

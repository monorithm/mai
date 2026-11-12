"""The shared steps of the mail tools: the session of a call, the mailbox scan, the ref lookup and the message views."""

from __future__ import annotations

import email
import email.header
import email.policy

import anyio
from bustan import Injectable

from mai.modules.sessions import SessionService
from mai.platform.mailserver import sender_proved

from ..domain import mail_rules as r
from ..domain.errors import MailError
from ..domain.ports import MailboxAccess


def _decode(value: str | None) -> str:
    if not value:
        return ""
    try:
        return str(email.header.make_header(email.header.decode_header(value)))
    except Exception:  # noqa: BLE001
        return value


def body_of(raw: bytes | None) -> str:
    msg = email.message_from_bytes(raw or b"", policy=email.policy.default)
    part = msg.get_body(preferencelist=("plain",)) if msg.is_multipart() else msg
    if part is None:
        return ""
    try:
        text = part.get_content()
    except Exception:  # noqa: BLE001
        # With decode=True the payload of a single part is bytes, else None.
        payload = part.get_payload(decode=True)
        data = payload if isinstance(payload, bytes) else b""
        text = data.decode("utf-8", "replace")
    return text if isinstance(text, str) else ""


def queue_of(state) -> str | None:
    return r.queue_of(getattr(state, "kind", "session"), state.agent, state.login)


def boxes_of(state, **kw) -> list[str]:
    return r.mailboxes(queue_of(state), **kw)


@Injectable()
class MailReader:
    def __init__(self, sessions: SessionService, access: MailboxAccess) -> None:
        self._sessions = sessions
        self._access = access
        self._locks: dict[str, anyio.Lock] = {}

    async def state_for(self, ctx, args: dict):
        """Find the session of a call: the argument "session" first, then the stdio state of ctx."""
        handle = args.get("session") if isinstance(args, dict) else None
        if handle:
            return await self._sessions.resolve(handle)
        state = getattr(ctx, "session_state", None)
        if state is not None:
            return await self._sessions.resolve(state.handle)
        if getattr(ctx, "transport", None) == "stdio":
            raise MailError("NOT_LOGGED_IN")
        raise MailError("SESSION_UNKNOWN", path="session")

    def lock(self, state) -> anyio.Lock:
        """One lock for each login serializes the IMAP work of its calls."""
        return self._locks.setdefault(state.login, anyio.Lock())

    async def call(self, state, method: str, *args, **kwargs):
        return await self._access.call(state.mail, method, *args, **kwargs)

    async def scan_box(self, state, box: str, criteria="ALL") -> list[dict]:
        try:
            uids = await self.call(state, "search", box, criteria)
        except MailError:
            raise
        except Exception:  # noqa: BLE001
            # A mailbox that does not exist yet (for example Done) has no messages.
            return []
        if not uids:
            return []
        entries = []
        for item in await self.call(state, "fetch_headers", box, list(uids)):
            raw_headers = {k.lower(): v for k, v in (item.get("headers") or {}).items()}
            headers = {
                k: (v[0] if isinstance(v, (list, tuple)) and v else v)
                for k, v in raw_headers.items()
            }
            headers = {
                k: ("" if v is None or isinstance(v, (list, tuple)) else str(v))
                for k, v in headers.items()
            }
            mid = (headers.get("message-id") or "").strip()
            if not mid:
                continue
            entries.append(
                {
                    "mailbox": box,
                    "uid": item["uid"],
                    "flags": [str(f) for f in (item.get("flags") or ())],
                    "headers": headers,
                    "raw_headers": raw_headers,
                    "message_id": mid,
                    "ref": r.ref_of(mid),
                    "root": r.thread_root(headers),
                    "size": item.get("size"),
                }
            )
        return entries

    async def scan(self, state, boxes, criteria="ALL") -> list[dict]:
        entries = []
        for box in boxes:
            entries.extend(await self.scan_box(state, box, criteria))
        return entries

    async def find(self, state, ref, boxes=None) -> dict:
        """Find the first entry with the ref, in mailbox order. The scan stops at the first mailbox that has it."""
        r.check_ref(ref)
        for box in boxes or boxes_of(state, done=True, sent=True):
            for entry in await self.scan_box(state, box):
                if entry["ref"] == ref:
                    return entry
        raise MailError("UNKNOWN_MESSAGE", path="ref")

    def summary(self, state, entry) -> dict:
        h = entry["headers"]
        try:
            proved = bool(sender_proved(entry.get("raw_headers") or h))
        except Exception:  # noqa: BLE001
            proved = False
        try:
            subject = _decode(h.get("subject"))
        except Exception:  # noqa: BLE001
            subject = h.get("subject") or ""
        item = (h.get("x-mai-item") or "").strip() or None
        return {
            "ref": entry["ref"],
            "thread_ref": r.thread_ref_of(entry["root"]),
            "from": r.one_addr(h.get("from")),
            "sender_proved": proved,
            "to": r.addr_list(h.get("to")),
            "subject": subject[: r.MAX_SUBJECT],
            "kind": r.vocab(h.get("x-mai-kind"), r.KINDS),
            "priority": r.vocab(h.get("x-mai-priority"), r.PRIORITIES),
            "date": r.parse_date(h.get("date")),
            "status": r.status_of(entry["flags"], entry["mailbox"]),
            "in_queue": entry["mailbox"] == queue_of(state),
            "item": item if item and r.ITEM_RE.match(item) else None,
        }

    async def full(self, state, entry) -> dict:
        message = self.summary(state, entry)
        raw = await self.call(state, "fetch_raw", entry["mailbox"], entry["uid"])
        h = entry["headers"]
        parent = r.msg_ids(h.get("in-reply-to"))
        expects = (h.get("x-mai-expects-reply") or "").strip().lower()
        message.update(
            {
                "body": body_of(raw),
                "cc": r.addr_list(h.get("cc")),
                "in_reply_to": r.ref_of(parent[0]) if parent else None,
                "expects_reply": expects in ("yes", "true", "1"),
            }
        )
        return message


def by_date(entries, newest_first=True):
    return sorted(
        entries,
        key=lambda e: r.parse_date(e["headers"].get("date")) or "",
        reverse=newest_first,
    )

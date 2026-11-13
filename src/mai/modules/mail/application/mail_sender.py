"""The delivery step of send_message and reply: the retry key, the message, the SMTP send and the copy in Sent."""

from __future__ import annotations

import datetime as dt
import email.policy
import email.utils
import logging
import uuid
from email.message import EmailMessage

import anyio
from bustan import Injectable

from ..domain import mail_rules as r
from ..domain.errors import MailError
from ..domain.ports import Directory, SentRequests
from .mail_reader import MailReader

log = logging.getLogger("mai.mail")


def build(
    address,
    to,
    cc,
    subject,
    body,
    kind,
    priority,
    expects,
    item,
    in_reply_to=None,
    references=None,
):
    msg = EmailMessage(policy=email.policy.SMTP)
    message_id = f"<msg-{uuid.uuid4()}@{r.DOMAIN}>"
    now = dt.datetime.now(dt.UTC).replace(microsecond=0)
    msg["From"] = address
    msg["To"] = ", ".join(f"{l}@{r.DOMAIN}" for l in to)
    if cc:
        msg["Cc"] = ", ".join(f"{l}@{r.DOMAIN}" for l in cc)
    msg["Subject"] = subject
    msg["Date"] = email.utils.format_datetime(now)
    msg["Message-ID"] = message_id
    msg["X-Mai-Kind"] = kind
    msg["X-Mai-Priority"] = priority
    msg["X-Mai-Expects-Reply"] = "yes" if expects else "no"
    if item:
        msg["X-Mai-Item"] = item
    if in_reply_to:
        msg["In-Reply-To"] = in_reply_to
    if references:
        msg["References"] = " ".join(references)
    msg.set_content(body)
    return msg.as_bytes(), message_id, now


@Injectable()
class MailSender:
    def __init__(
        self, reader: MailReader, sent: SentRequests, directory: Directory
    ) -> None:
        self._reader = reader
        self._sent = sent
        self._directory = directory

    async def check_known(self, logins: list[str], path: str) -> None:
        if not logins:
            return
        known = await self._directory.known_logins(logins)
        for login in logins:
            if login not in known:
                raise MailError("RECIPIENT_UNKNOWN", path=path)

    async def deliver(
        self,
        state,
        to,
        cc,
        subject,
        body,
        kind,
        priority,
        expects,
        item,
        request_id,
        in_reply_to=None,
        references=None,
    ) -> dict:
        fields = {
            "to": to,
            "cc": cc,
            "subject": subject,
            "body": body,
            "kind": kind,
            "priority": priority,
            "expects_reply": expects,
            "item": item,
            "in_reply_to": in_reply_to,
        }
        sha = r.content_sha(fields)
        root = references[0] if references else None
        addresses = [f"{l}@{r.DOMAIN}" for l in to]
        raw, message_id, now = build(
            state.address,
            to,
            cc,
            subject,
            body,
            kind,
            priority,
            expects,
            item,
            in_reply_to,
            references,
        )
        # The lock of the login keeps two calls with the same key apart: the
        # second call reserves after the first call knows its result.
        async with self._reader.lock(state):
            kept = await self._sent.reserve(state.login, request_id, sha, message_id)
            if kept.duplicate:
                result = {
                    "ref": r.ref_of(kept.message_id),
                    "thread_ref": r.thread_ref_of(root or kept.message_id),
                    "message_id": kept.message_id,
                    "to": addresses,
                    "duplicate": True,
                }
                if kept.sent_at is not None:
                    result["date"] = r.iso(kept.sent_at)
                return result
            try:
                await self._reader.call(
                    state,
                    "send",
                    raw,
                    state.address,
                    [f"{l}@{r.DOMAIN}" for l in to + cc],
                )
            except BaseException:
                # The send failed, thus a retry with the same key must send again.
                with anyio.CancelScope(shield=True):
                    await self._keep(self._sent.release, state, request_id, message_id)
                raise
            await self._keep(self._sent.mark_sent, state, request_id, message_id, now)
            try:
                await self._reader.call(state, "append", "Sent", raw, flags=(r.SEEN,))
            except Exception:  # noqa: BLE001, S110
                # The mail is sent; a copy in Sent is a convenience.
                pass
        return {
            "ref": r.ref_of(message_id),
            "thread_ref": r.thread_ref_of(root or message_id),
            "message_id": message_id,
            "to": addresses,
            "date": r.iso(now),
            "duplicate": False,
        }

    @staticmethod
    async def _keep(step, state, request_id, message_id, *args) -> None:
        """Write the result of a send to the retry key. A failure of the store is not the result of the call.

        If the write fails, the key stays pending, and the next reserve of the
        key decides. The PostgreSQL store looks for the envelope of the mail:
        with an envelope the retry gets a duplicate, with no envelope the
        retry sends.
        """
        try:
            await step(state.login, request_id, message_id, *args)
        except Exception as exc:  # noqa: BLE001 - the send gives the result of the call
            log.error(
                "the retry key of %s did not change: %s",
                state.login,
                type(exc).__name__,
            )

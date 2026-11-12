"""Calls to the synchronous MailSession (imaplib and smtplib) in a worker thread."""

from __future__ import annotations

import functools
import imaplib
import smtplib

import anyio.to_thread
from bustan import Injectable

from ..domain.errors import MailError
from ..domain.ports import MailboxAccess

_LOST = (OSError, EOFError, imaplib.IMAP4.abort, smtplib.SMTPServerDisconnected)


def _call_sync(mail, method, args, kwargs):
    fn = getattr(mail, method)
    try:
        return fn(*args, **kwargs)
    except MailError:
        raise
    except _LOST:
        # One reconnect, then the call again.
        try:
            if hasattr(mail, "connect"):
                mail.connect()
                return fn(*args, **kwargs)
        except MailError:
            raise
        except Exception as exc:
            raise MailError("MAIL_SERVER_DOWN") from exc
        raise MailError("MAIL_SERVER_DOWN") from None


@Injectable()
class ThreadedMailboxAccess(MailboxAccess):
    async def call(self, mail, method, *args, **kwargs):
        return await anyio.to_thread.run_sync(
            functools.partial(_call_sync, mail, method, args, kwargs)
        )

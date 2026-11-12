"""The factory that opens a MailSession for one login on Dovecot and Postfix."""

from __future__ import annotations

import os

from .mail_session import DEFAULT_TIMEOUT, MailSession


class MailClientFactory:
    """Opens a MailSession with the hosts and ports of this process.

    The default values come from the environment: MAIL_IMAP_HOST,
    MAIL_IMAP_PORT, MAIL_SMTP_HOST and MAIL_SMTP_PORT. In the container,
    Dovecot listens on 127.0.0.1:1143 and Postfix submission on 127.0.0.1:1588.
    A test gives other values, or replaces the factory with a fake.
    """

    def __init__(
        self,
        imap_host: str | None = None,
        imap_port: int | None = None,
        smtp_host: str | None = None,
        smtp_port: int | None = None,
        timeout: float = DEFAULT_TIMEOUT,
    ) -> None:
        self.imap_host = imap_host or os.environ.get("MAIL_IMAP_HOST", "127.0.0.1")
        self.imap_port = int(imap_port or os.environ.get("MAIL_IMAP_PORT", "1143"))
        self.smtp_host = smtp_host or os.environ.get("MAIL_SMTP_HOST", "127.0.0.1")
        self.smtp_port = int(smtp_port or os.environ.get("MAIL_SMTP_PORT", "1588"))
        self.timeout = timeout

    def open(self, login: str, secret: str) -> MailSession:
        """Return a MailSession that is not connected yet.

        The caller calls connect(), or uses the session as a context manager,
        and closes it. The session connects on its first command if the caller
        does not call connect().
        """
        return MailSession(
            login,
            secret,
            imap_host=self.imap_host,
            imap_port=self.imap_port,
            smtp_host=self.smtp_host,
            smtp_port=self.smtp_port,
            timeout=self.timeout,
        )

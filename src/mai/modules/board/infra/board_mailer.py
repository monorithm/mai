"""Sends board mail as board@mai.internal with the board credential. One connection, reused."""

import logging
import os
import re
import threading
from pathlib import Path

from ..domain import board_rules as B

log = logging.getLogger("mai.board")


def read_board_credential(path):
    """Read a credential file of the form mai1:<login>:<secret>. Return (login, secret) or raise ValueError."""
    parts = Path(path).expanduser().read_text(encoding="utf-8").strip().split(":")
    if (
        len(parts) != 3
        or parts[0] != "mai1"
        or not re.fullmatch(r"[a-z2-7]{20,}", parts[2])
    ):
        raise ValueError("not a credential")
    return parts[1].split("@")[0], parts[2]


class BoardMailer:
    """factory is the MailClientFactory of the mail server platform."""

    def __init__(self, factory, credential_path=None):
        self._factory = factory
        self._path = credential_path or os.environ.get(
            "MAI_BOARD_CRED",
            os.path.join(os.environ.get("MAI_SECRETS", "/data/secrets"), "board.cred"),
        )
        self._lock = threading.Lock()
        self._session = None

    def _open(self):
        login, secret = read_board_credential(self._path)
        session = self._factory.open(login, secret)
        session.connect()
        return session

    def send(self, raw, rcpt):
        """Send one raw mail. Return False after two failed attempts; never raise."""
        with self._lock:
            for attempt in (0, 1):
                try:
                    if self._session is None:
                        self._session = self._open()
                    self._session.send(
                        raw, B.address(B.BOARD_LOGIN), [B.address(r) for r in rcpt]
                    )
                    return True
                except Exception:  # noqa: BLE001 - a failed mail or close never fails the tool call
                    try:
                        if self._session is not None:
                            self._session.close()
                    except Exception:  # noqa: BLE001, S110 - a failed mail or close never fails the tool call
                        pass
                    self._session = None
                    if attempt:
                        log.warning("board mail not sent")
        return False

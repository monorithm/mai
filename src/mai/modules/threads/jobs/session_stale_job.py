"""The session_stale signal: tell the supervisor when the lease of a claim ended.

Each minute the job looks for claims whose lease ended and that have no stale
signal yet. For each one it records the signal first (thus a second process
or a second run sends no copy) and then sends one mail from board@ with
X-Mai-Kind: signal to the supervisor. THREADS_SUPERVISOR names the login of
the supervisor (default "owner").
"""

from __future__ import annotations

import asyncio
import logging
import os

import anyio.to_thread
from bustan import Injectable

from mai.platform.contract import background_on
from mai.platform.mailserver import (
    MailClientFactory,
    address_of,
    build_message,
    thread_ref_of,
)

from ..domain.claim import Claim, ClaimStore, iso
from ..domain.credentials import CredentialReader
from ..domain.returner import ThreadMailReturner

log = logging.getLogger("mai.session_stale")

INTERVAL_SECONDS = 60.0


def stale_body(claim: Claim) -> str:
    return (
        f"session_stale: the claim of {claim.login} on {thread_ref_of(claim.root_msg_id)}"
        f" of {claim.agent} ended at {iso(claim.lease_end)}.\n"
    )


@Injectable()
class SessionStaleJob:
    def __init__(
        self,
        store: ClaimStore,
        mail_factory: MailClientFactory,
        credentials: CredentialReader,
        returner: ThreadMailReturner,
    ) -> None:
        self._credentials = credentials
        self._returner = returner
        self._store = store
        self._mail_factory = mail_factory
        self.credential_file = os.environ.get(
            "MAI_BOARD_CRED", "/data/secrets/board.cred"
        )
        self.supervisor = os.environ.get("THREADS_SUPERVISOR", "owner")
        self.enabled = (
            background_on() and os.environ.get("THREADS_STALE_SIGNAL", "on") != "off"
        )
        self._task: asyncio.Task | None = None

    def _send(self, claims: list[Claim]) -> None:
        login, secret = self._credentials.read(self.credential_file)
        with self._mail_factory.open(login, secret) as mail:
            for claim in claims:
                raw = _build(login, self.supervisor, claim)
                mail.send(raw, address_of(login), [address_of(self.supervisor)])

    async def run_once(self) -> list[Claim]:
        """Give back the unread mail of each claim whose lease ended, and signal it, once.

        Give the claims that this run signalled. The mail goes back under the
        lock of the claim, thus a new claim of the thread waits for it.
        """
        due = []
        for c in await self._store.ended_unsignalled():
            async with self._store.locked(c.agent, c.root_msg_id) as txn:
                row = await txn.get()
                if row is None or row.login != c.login or row.live(await txn.now()):
                    continue  # a new claim of the thread took it
                if not await anyio.to_thread.run_sync(
                    self._returner, c.login, c.agent, c.root_msg_id
                ):
                    log.warning(
                        "session_stale: the unread mail of %s did not go back", c.login
                    )
                    if await self._store.session_live(c.login):
                        continue  # the next run tries again
                    # A revoked session has no queue rights any more, thus no run can return
                    # its mail: the revoke returned it before. Signal the claim once.
            if await self._store.mark_signalled(c):
                due.append(c)
        if due:
            await anyio.to_thread.run_sync(self._send, due)
        return due

    async def _loop(self) -> None:
        while True:
            await asyncio.sleep(INTERVAL_SECONDS)
            try:
                await self.run_once()
            except Exception as exc:  # noqa: BLE001 - a notice, a hook or a daemon loop must outlive any error
                log.warning("session_stale: %s", type(exc).__name__)

    async def on_application_bootstrap(self) -> None:
        if self.enabled:
            self._task = asyncio.create_task(self._loop())

    async def on_application_shutdown(self, signal=None) -> None:
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None


def _build(from_login: str, to_login: str, claim: Claim) -> bytes:
    return build_message(
        from_login,
        None,
        [to_login],
        subject=f"session_stale {thread_ref_of(claim.root_msg_id)}",
        body=stale_body(claim),
        kind="signal",
        priority="normal",
    )

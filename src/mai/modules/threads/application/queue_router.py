"""The router: it moves replies in a claimed thread from the agent queue to the session inbox.

Each agent queue has one worker thread with one IMAP connection as the board
account. The worker waits with IDLE on that one mailbox and reads the headers
of each new mail. If a live claim holds the thread root, the mail goes to the
INBOX of the session that holds the claim. A lost connection reconnects after
a delay that doubles after each failure, up to MAX_BACKOFF, and goes back to
the first delay after a good connection. Thus the router never opens a new
login for each wait.
"""

from __future__ import annotations

import asyncio
import concurrent.futures
import contextlib
import imaplib
import logging
import math
import os
import threading
import time

import anyio.to_thread
from bustan import Injectable

from mai.modules.credentials import CredentialService
from mai.platform.contract import background_on
from mai.platform.mailserver import MailClientFactory, parse_summary

from ..domain.claim import ClaimStore, queue_of
from ..domain.credentials import CredentialReader
from ..domain.mover import QueueMover

log = logging.getLogger("mai.router")

# IDLE can miss a notice that TLS holds in its buffer, thus each wait is short and ends in a scan.
IDLE_TIMEOUT = 2.0
FIRST_BACKOFF = 1.0
MAX_BACKOFF = 300.0


# The errors of a lost connection to Dovecot or to the app loop. Other errors are of one mail.
CONNECTION_ERRORS = (OSError, imaplib.IMAP4.abort, concurrent.futures.TimeoutError)
# The time after which a mail that stays in the queue is examined again.
RECHECK_AFTER = 30.0
# The maximum time of one route: the claim lock, the session check, and the move.
ROUTE_TIMEOUT = 60.0
# The wait before the next look for the board credential, at the first start.
CREDENTIAL_WAIT = 1.0
# The default time between two reads of the agent list. A new agent gets its
# worker after this time. THREADS_AGENT_REFRESH (seconds) changes it.
AGENT_REFRESH = 30.0


class CredentialNotReady(Exception):
    """The board credential file does not exist yet: the accounts are not made."""


def _is_connection_error(exc: BaseException) -> bool:
    """A lost connection to Dovecot or to the app loop. Any other error is of one mail only."""
    if isinstance(exc, CONNECTION_ERRORS):
        return True
    return getattr(exc, "code", None) in ("MAIL_SERVER_DOWN", "STORE_UNAVAILABLE")


def _refresh_time(value: str | None) -> float:
    """The refresh time of the agent list: the value in seconds, or AGENT_REFRESH."""
    try:
        seconds = float(value) if value else AGENT_REFRESH
    except ValueError:
        return AGENT_REFRESH
    return seconds if seconds > 0 else AGENT_REFRESH


def next_backoff(current: float) -> float:
    return min(MAX_BACKOFF, max(FIRST_BACKOFF, current * 2))


class QueueWorker:
    """The router of one agent queue: one board connection, IDLE on one mailbox.

    route(agent, root, uid) moves the mail to the session that holds a live
    claim on the thread, and gives that session, or None. It holds the lock
    of the claim during the move, thus a release cannot end the claim between
    the look-up and the move.

    seen maps each UID that the worker examined to the time of the look. A
    UID is examined again after RECHECK_AFTER, while it stays in the queue:
    a move that did not take the mail out, or a claim that came later, thus
    gets a new try. A UID with an error at its look is examined again at the
    next scan.
    """

    def __init__(self, agent, open_mail, route, stop: threading.Event) -> None:
        self.agent = agent
        self.mailbox = queue_of(agent)
        self._open_mail = open_mail
        self._route = route
        self._stop = stop
        self.seen: dict[int, float] = {}
        self.mail = None
        self.logins = 0
        self._no_access_logged = False

    def scan(self) -> list[tuple[str, str, int]]:
        """Look at each new or due mail in the queue. Give the (agent, session, uid) of each move."""
        mail = self.mail
        assert mail is not None, "run opens the mail session before each scan"
        uids = mail.search(self.mailbox, "ALL")
        now = time.monotonic()
        present = set(uids)
        self.seen = {u: t for u, t in self.seen.items() if u in present}
        due = [u for u in uids if now - self.seen.get(u, -math.inf) >= RECHECK_AFTER]
        moves = []
        if not due:
            return moves
        for row in mail.fetch_headers(self.mailbox, due):
            uid = row.get("uid")
            try:
                summary = parse_summary(row.get("headers") or {})
                root = summary.get("root_message_id")
                if not root or root == summary.get("message_id"):
                    # A mail that starts a thread stays in the queue. A claim moves it, not the router.
                    self.seen[uid] = math.inf
                    continue
                session = self._route(self.agent, root, uid)
                if session and mail.contains_uid(self.mailbox, uid):
                    # doveadm move exits 0 also when it moved nothing.
                    log.warning(
                        "router %s: a move to %s left the mail in the queue; it is tried again",
                        self.agent,
                        session,
                    )
                    session = None
            except Exception as exc:
                if not _is_connection_error(exc):
                    log.warning(
                        "router %s: a mail gave %s; it is examined again later",
                        self.agent,
                        getattr(exc, "code", None) or type(exc).__name__,
                    )
                    self.seen[uid] = now
                    continue
                # The connection is lost: the next scan, after the reconnect, looks again.
                self.seen.pop(uid, None)
                raise
            self.seen[uid] = now
            if session:
                moves.append((self.agent, session, uid))
                log.info("router: moved a reply of a claimed thread of %s", self.agent)
        return moves

    def run(self) -> None:
        backoff = 0.0
        while not self._stop.is_set():
            try:
                self.mail = self._open_mail()
                self.logins += 1
                self.scan()
                backoff = 0.0
                while not self._stop.is_set():
                    # A scan after a timeout also catches a mail that came during a reconnect.
                    started = time.monotonic()
                    new_mail = self.mail.idle([self.mailbox], IDLE_TIMEOUT)
                    if self._stop.is_set():
                        break
                    if not new_mail and time.monotonic() - started < IDLE_TIMEOUT / 2:
                        # IDLE did not wait: the queue is not open for this login. Do not spin.
                        if not self._no_access_logged:
                            log.warning(
                                "router %s: the board cannot open the queue; the mailbox grant is missing",
                                self.agent,
                            )
                            self._no_access_logged = True
                        self._stop.wait(IDLE_TIMEOUT)
                    self.scan()
            except CredentialNotReady:
                # At the first start the app can start before the accounts exist. This is not an error.
                log.debug(
                    "router %s: the board credential does not exist yet", self.agent
                )
                self._stop.wait(CREDENTIAL_WAIT)
            except Exception as exc:  # noqa: BLE001 - a notice, a hook or a daemon loop must outlive any error
                backoff = next_backoff(backoff)
                if not self._stop.is_set():
                    log.warning(
                        "router %s: %s; retry in %s s",
                        self.agent,
                        getattr(exc, "code", None) or type(exc).__name__,
                        backoff,
                    )
                self._stop.wait(backoff)
            finally:
                self.close()

    def close(self) -> None:
        mail, self.mail = self.mail, None
        if mail is not None:
            try:
                mail.close()
            except Exception:  # noqa: BLE001, S110 - a notice, a hook or a daemon loop must outlive any error
                pass


@Injectable()
class QueueRouter:
    """Starts one QueueWorker thread for each agent, and stops them at shutdown.

    The agents are the active agent accounts of the credential store. The
    router reads them at bootstrap and again each AGENT_REFRESH seconds, and
    starts a worker for each agent that has no worker yet.
    """

    def __init__(
        self,
        store: ClaimStore,
        mail_factory: MailClientFactory,
        mover: QueueMover,
        credentials: CredentialReader,
        accounts: CredentialService,
    ) -> None:
        self._store = store
        self._mail_factory = mail_factory
        self._credentials = credentials
        self._accounts = accounts
        self.credential_file = os.environ.get(
            "MAI_BOARD_CRED", "/data/secrets/board.cred"
        )
        self.enabled = (
            background_on() and os.environ.get("THREADS_ROUTER", "on") != "off"
        )
        self.agents: list[str] = []
        self.refresh_every = _refresh_time(os.environ.get("THREADS_AGENT_REFRESH"))
        self.mover = mover
        self._stop = threading.Event()
        self.workers: list[QueueWorker] = []
        self._threads: list[threading.Thread] = []
        self._loop: asyncio.AbstractEventLoop | None = None
        self._refresh_task: asyncio.Task | None = None

    def _open_mail(self):
        if not os.path.exists(self.credential_file):
            raise CredentialNotReady(self.credential_file)
        login, secret = self._credentials.read(self.credential_file)
        session = self._mail_factory.open(login, secret)
        # The router only reads and moves: it needs no SMTP connection.
        session.connect_imap()
        return session

    def _route(self, agent: str, root: str, uid: int) -> str | None:
        # The workers are plain threads: they run the async part on the loop of the application.
        loop = self._loop
        assert loop is not None, "the workers start after bootstrap sets the loop"
        future = asyncio.run_coroutine_threadsafe(
            self._route_locked(agent, root, uid), loop
        )
        try:
            return future.result(timeout=ROUTE_TIMEOUT)
        except concurrent.futures.TimeoutError:
            # Stop the route on the loop: the next look of the mail must not run beside it.
            future.cancel()
            raise

    async def _route_locked(self, agent: str, root: str, uid: int) -> str | None:
        # The lock of (agent, root) is the lock of claim and release: the claim cannot end during the move.
        async with self._store.locked(agent, root) as txn:
            claim = await txn.get()
            if claim is None or not claim.live(await txn.now()):
                return None
            if not await self._store.session_live(claim.login):
                return None
            moved = await anyio.to_thread.run_sync(self.mover, claim.login, agent, uid)
            return claim.login if moved else None

    def _start_worker(self, agent: str) -> None:
        worker = QueueWorker(agent, self._open_mail, self._route, self._stop)
        thread = threading.Thread(
            target=worker.run, name=f"mai-router-{agent}", daemon=True
        )
        self.agents.append(agent)
        self.workers.append(worker)
        self._threads.append(thread)
        thread.start()

    async def refresh(self) -> list[str]:
        """Read the agent list and start a worker for each new agent. Give the new agents."""
        try:
            names = await self._accounts.agent_names()
        except Exception as exc:  # noqa: BLE001 - the next refresh tries again
            log.warning(
                "router: cannot read the agent list: %s",
                getattr(exc, "code", None) or type(exc).__name__,
            )
            return []
        new = [name for name in names if name not in self.agents]
        for agent in new:
            self._start_worker(agent)
        if new:
            log.info("router: started the queue workers of %s", ", ".join(new))
        return new

    async def _refresh_loop(self) -> None:
        while not self._stop.is_set():
            await asyncio.sleep(self.refresh_every)
            await self.refresh()

    async def on_application_bootstrap(self) -> None:
        if not self.enabled:
            return
        self._loop = asyncio.get_running_loop()
        self._stop.clear()
        await self.refresh()
        self._refresh_task = self._loop.create_task(self._refresh_loop())

    async def on_application_shutdown(self, signal=None) -> None:
        self._stop.set()
        task, self._refresh_task = self._refresh_task, None
        if task is not None:
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await task
        for worker in self.workers:
            # Closing the socket ends a wait in IDLE.
            worker.close()
        for thread in self._threads:
            await anyio.to_thread.run_sync(thread.join, 5)
        self.workers.clear()
        self._threads.clear()
        self.agents.clear()

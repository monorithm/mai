from typing import Any

import anyio.to_thread

from ..domain.new_mail_counter import NewMailCounter
from ..domain.session_state import SessionState

# The maximum wait for a tool call of the session that holds its lock.
LOCK_WAIT_SECONDS = 2.0


class ImapNewMailCounter(NewMailCounter):
    """Counts the mail with the same IMAP search as the notices of the watch command."""

    async def count(self, state: SessionState) -> int | None:
        # The notices module imports the threads module, which imports this
        # module. A late import prevents the import cycle.
        from mai.modules.notices import SEARCH, mailboxes_of

        boxes = mailboxes_of(state.login, frozenset({state.agent}))

        mail: Any = state.mail

        def run() -> int | None:
            # A tool call of the session that holds the lock gives no count.
            if not state.lock.acquire(timeout=LOCK_WAIT_SECONDS):
                return None
            try:
                total = 0
                for mailbox in boxes:
                    total += len(mail.search(mailbox, SEARCH) or [])
                return total
            finally:
                state.lock.release()

        try:
            # A time limit of the caller does not wait for the thread.
            return await anyio.to_thread.run_sync(run, abandon_on_cancel=True)
        except Exception:  # noqa: BLE001 - a failed count must not end the heartbeat
            return None

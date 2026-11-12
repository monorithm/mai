from typing import Any

import anyio.to_thread

from ..domain.new_mail_counter import NewMailCounter
from ..domain.session_state import SessionState


class ImapNewMailCounter(NewMailCounter):
    """Counts the mail with the same IMAP search as the notices of the watch command."""

    async def count(self, state: SessionState) -> int:
        # The notices module imports the threads module, which imports this
        # module. A late import prevents the import cycle.
        from mai.modules.notices import SEARCH, mailboxes_of

        boxes = mailboxes_of(state.login, frozenset({state.agent}))

        mail: Any = state.mail

        def run() -> int:
            total = 0
            with state.lock:
                for mailbox in boxes:
                    total += len(mail.search(mailbox, SEARCH) or [])
            return total

        try:
            return await anyio.to_thread.run_sync(run)
        except Exception:  # noqa: BLE001 - a failed count must not end the heartbeat
            return 0

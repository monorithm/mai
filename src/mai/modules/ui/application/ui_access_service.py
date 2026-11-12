"""Turn the UI on or off, make sign-in tickets, and change a ticket into a session."""

from __future__ import annotations

from dataclasses import dataclass

from bustan import Injectable

from mai.platform.clock import Clock

from ..domain.owner_session import (
    UI_TTL,
    OwnerSession,
    OwnerSessionStore,
    new_csrf,
    new_token,
    token_key,
)
from ..domain.sign_in_ticket import (
    TICKET_TTL,
    SignInTicketStore,
    new_ticket,
    ticket_key,
)
from ..domain.ui_state import UiOffError, UiStateStore


@dataclass(frozen=True, slots=True)
class IssuedTicket:
    ticket: str
    expires_seconds: int


@dataclass(frozen=True, slots=True)
class SignInResult:
    token: str
    csrf: str
    max_age: int


@Injectable()
class UiAccessService:
    def __init__(
        self,
        state: UiStateStore,
        tickets: SignInTicketStore,
        sessions: OwnerSessionStore,
        clock: Clock,
    ) -> None:
        self._state = state
        self._tickets = tickets
        self._sessions = sessions
        self._clock = clock

    async def enabled(self) -> bool:
        return await self._state.enabled()

    async def set_enabled(self, enabled: bool) -> bool:
        """Set the state. When the UI goes off, all tickets and sessions end."""
        await self._state.set_enabled(enabled)
        if not enabled:
            await self._tickets.clear()
            await self._sessions.clear()
        return enabled

    async def issue_ticket(self) -> IssuedTicket:
        """Make a ticket that is valid one time, for 60 s. Raise UiOffError when the UI is off."""
        if not await self._state.enabled():
            raise UiOffError()
        ticket = new_ticket()
        await self._tickets.save(ticket_key(ticket), self._clock.now() + TICKET_TTL)
        return IssuedTicket(ticket, int(TICKET_TTL.total_seconds()))

    async def redeem(self, ticket: str) -> SignInResult | None:
        """Use the ticket one time. Return the new session, or None."""
        if not ticket or not await self._state.enabled():
            return None
        now = self._clock.now()
        if not await self._tickets.take(ticket_key(ticket), now):
            return None
        await self._sessions.purge_expired(now)
        token, csrf = new_token(), new_csrf()
        await self._sessions.save(
            OwnerSession(token_key(token), csrf, now + UI_TTL, now)
        )
        return SignInResult(token, csrf, int(UI_TTL.total_seconds()))

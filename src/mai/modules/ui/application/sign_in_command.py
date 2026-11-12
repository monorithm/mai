"""Sign the owner in with the owner credential."""

from __future__ import annotations

from dataclasses import dataclass

from bustan import Injectable

from mai.modules.credentials import CredentialService
from mai.platform.clock import Clock

from ..domain.owner_session import (
    UI_TTL,
    OwnerSession,
    OwnerSessionStore,
    new_csrf,
    new_token,
    token_key,
)


@dataclass(frozen=True, slots=True)
class SignInCommand:
    credential: str


@dataclass(frozen=True, slots=True)
class SignInResult:
    token: str
    csrf: str
    max_age: int


@Injectable()
class SignInHandler:
    def __init__(
        self, credentials: CredentialService, store: OwnerSessionStore, clock: Clock
    ) -> None:
        self._credentials = credentials
        self._store = store
        self._clock = clock

    async def execute(self, command: SignInCommand) -> SignInResult | None:
        """Return the new session, or None for a credential that is not the owner's."""
        if not command.credential.strip():
            return None
        if (
            await self._credentials.authenticate_owner(command.credential.strip())
            is None
        ):
            return None
        now = self._clock.now()
        await self._store.purge_expired(now)
        token, csrf = new_token(), new_csrf()
        await self._store.save(OwnerSession(token_key(token), csrf, now + UI_TTL))
        return SignInResult(token, csrf, int(UI_TTL.total_seconds()))

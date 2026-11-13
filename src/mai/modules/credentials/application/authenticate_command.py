import hashlib
from dataclasses import dataclass
from datetime import UTC, datetime

from bustan import Injectable

from ..domain.credential_store import CredentialStore
from ..domain.errors import CredentialError
from ..domain.secret_hasher import SecretHasher


@dataclass(frozen=True)
class AuthenticateCommand:
    login: str
    secret: str


@Injectable()
class AuthenticateHandler:
    """Checks a login and a secret and marks the login active.

    Returns the principal with no hash. An agent account has no secret and
    cannot log in. Raises CredentialError with the code of the failure.
    """

    def __init__(self, store: CredentialStore, hasher: SecretHasher) -> None:
        self._store = store
        self._hasher = hasher

    async def execute(self, command: AuthenticateCommand) -> dict:
        now = datetime.now(UTC)
        p = await self._store.get(command.login)
        if p is None or p.kind == "agent" or not p.password_hash:
            raise CredentialError("LOGIN_FAILED")
        if not self._hasher.verify(command.secret, p.password_hash):
            raise CredentialError("LOGIN_FAILED")
        if p.status == "revoked":
            raise CredentialError("CREDENTIAL_REVOKED")
        if p.is_expired(now):
            if p.status != "expired":
                await self._store.set_status(command.login, "expired", now)
            raise CredentialError("CREDENTIAL_EXPIRED")
        if not await self._store.mark_login(command.login, now):
            # A revoke came after the read of the principal.
            raise CredentialError("CREDENTIAL_REVOKED")
        result = p.with_changes(status="active", last_login_at=now).public()
        result["fingerprint"] = fingerprint(p.password_hash)
        result["expires_at_value"] = p.expires_at
        return result


def fingerprint(password_hash: str | None) -> str | None:
    """A digest of the stored hash. It changes when the secret changes."""
    if not password_hash:
        return None
    return hashlib.sha256(password_hash.encode("utf-8")).hexdigest()

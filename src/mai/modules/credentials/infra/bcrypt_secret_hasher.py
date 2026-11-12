import bcrypt

from ..domain.secret_hasher import SecretHasher

SCHEME = "{BLF-CRYPT}"
ROUNDS = 10


class BcryptSecretHasher(SecretHasher):
    """bcrypt in the Dovecot passdb form "{BLF-CRYPT}$2y$..."."""

    def hash(self, secret: str) -> str:
        hashed = bcrypt.hashpw(
            secret.encode("utf-8"), bcrypt.gensalt(rounds=ROUNDS)
        ).decode("ascii")
        # bcrypt makes $2b$ and Dovecot expects $2y$. The two forms are the same hash.
        return SCHEME + "$2y$" + hashed[4:]

    def verify(self, secret: str, stored: str | None) -> bool:
        if not stored or not secret:
            return False
        value = stored.removeprefix(SCHEME)
        if value.startswith("$2y$"):
            value = "$2b$" + value[4:]
        try:
            return bcrypt.checkpw(secret.encode("utf-8"), value.encode("ascii"))
        except ValueError:
            return False

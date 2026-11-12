"""Errors of the credentials module. The code is a code of the tool contract."""


class CredentialError(Exception):
    """A login check failed. code is LOGIN_FAILED, CREDENTIAL_EXPIRED, or CREDENTIAL_REVOKED."""

    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


class UnknownAccountError(ValueError):
    """The agent or the session login does not exist."""


class AccountStateError(ValueError):
    """The operation is not possible in the current state of the account."""

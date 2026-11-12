"""The errors of the mail domain. The api layer changes them into tool errors."""


class MailError(Exception):
    """A refused mail operation, with the contract code, the argument path and extra fields."""

    def __init__(self, code: str, path: str | None = None, **extra) -> None:
        super().__init__(code)
        self.code = code
        self.path = path
        self.extra = extra

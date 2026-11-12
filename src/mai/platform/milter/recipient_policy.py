from __future__ import annotations

from .envelope import bare_address

COPIES_LOGIN = "copies"


class CopiesRecipientPolicy:
    """Refuses the address of the mailbox "copies" as a recipient.

    Postfix adds the copy recipient itself (always_bcc). A client that names
    the address could put mail into the copies of the owner, thus the milter
    refuses it. A bare local part ("copies") is in the mail domain, because
    Postfix appends the domain to it.
    """

    def __init__(self, domain: str = "mai.internal", login: str = COPIES_LOGIN) -> None:
        self._domain = domain.lower()
        self._login = login.lower()

    def check(self, recipient: str) -> str | None:
        """Returns None when the recipient is permitted, else the reason."""
        address = bare_address(recipient).lower()
        local, sep, domain = address.rpartition("@")
        if not sep:
            local, domain = address, self._domain
        local = local.strip('"')
        domain = domain.rstrip(".")
        if local == self._login and domain == self._domain:
            return "the copies mailbox is not a recipient"
        return None

from __future__ import annotations

from email.utils import getaddresses


class FromHeaderPolicy:
    """Decides if the From headers of a message match the SASL login.

    Postfix checks only the envelope sender against the login. This policy
    checks the From header too, so that an authenticated client cannot show
    a different author to the reader. The message must have exactly one From
    header with exactly one address, and that address must be
    <login>@<domain>. A display name is permitted ("Name <login@domain>").
    """

    def __init__(self, domain: str = "mai.internal") -> None:
        self._domain = domain.lower()

    def expected_address(self, login: str) -> str:
        login = login.strip().lower()
        if "@" in login:
            return login
        return f"{login}@{self._domain}"

    def check(self, login: str | None, from_values: list[str]) -> str | None:
        """Returns None when the message is permitted, else the reason."""
        if not login:
            return "no authenticated login"
        if len(from_values) != 1:
            return f"{len(from_values)} From headers, exactly 1 is required"
        value = " ".join(from_values[0].replace("\r", " ").replace("\n", " ").split())
        addresses = [addr for _, addr in getaddresses([value]) if addr]
        if len(addresses) != 1:
            return "the From header must hold exactly 1 address"
        if addresses[0].strip().lower() != self.expected_address(login):
            return "the From address does not match the authenticated login"
        return None

"""Keeps imported mail in memory. For tests and the memory backend."""

from __future__ import annotations

from bustan import Injectable

from ..domain.ports import MailImporter


@Injectable()
class MemoryMailImporter(MailImporter):
    def __init__(self) -> None:
        self.mails: dict[str, list[tuple[bytes, list[str], str | None]]] = {}

    async def append(
        self, account: str, raw: bytes, flags: list[str], message_id: str | None
    ) -> str:
        box = self.mails.setdefault(account, [])
        if message_id and any(m[2] == message_id for m in box):
            return "skipped"
        box.append((raw, list(flags), message_id))
        return "imported"

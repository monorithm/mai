"""A backup tool for the memory backend: the database part is a small marker file."""

from __future__ import annotations

from pathlib import Path

from bustan import Injectable

from ..domain.ports import BackupTool

MARKER = b"mai memory backend: no database dump\n"


@Injectable()
class MemoryBackupTool(BackupTool):
    def __init__(self) -> None:
        self.restored: list[Path] = []
        self.daemon_calls: list[str] = []

    async def dump_database(self, target: Path) -> None:
        target.write_bytes(MARKER)

    async def restore_database(self, source: Path) -> None:
        self.restored.append(source)

    async def stop_daemons(self) -> None:
        self.daemon_calls.append("stop")

    async def start_daemons(self) -> None:
        self.daemon_calls.append("start")

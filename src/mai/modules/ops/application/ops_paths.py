"""The directories that the ops work reads and writes."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

from ..domain.backups import KEEP_BACKUPS


@dataclass(frozen=True, slots=True)
class OpsPaths:
    backup_dir: Path
    mail_dir: Path
    keep: int = KEEP_BACKUPS

    @classmethod
    def from_environment(cls) -> OpsPaths:
        data = Path(os.environ.get("MAI_DATA", "/data"))
        return cls(backup_dir=data / "backup", mail_dir=data / "mail")

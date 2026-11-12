import datetime as dt
from pathlib import Path

import anyio
import pytest

from mai.modules.ops.application.ops_paths import OpsPaths
from mai.modules.ops.application.ops_service import OpsService
from mai.modules.ops.domain.ports import AccountDirectory
from mai.modules.ops.infra.fs_maildir_source import FsMaildirSource
from mai.modules.ops.infra.memory_backup_tool import MemoryBackupTool
from mai.modules.ops.infra.memory_mail_importer import MemoryMailImporter
from mai.platform.clock import FixedClock


def _mail(mid: str) -> bytes:
    return f"From: a@mai.internal\r\nTo: b@mai.internal\r\nMessage-ID: {mid}\r\nSubject: t\r\n\r\nbody\r\n".encode()


def make_old_maildir(tmp_path: Path) -> Path:
    """A 0.5.0 Maildir: one folder per address with cur, new and tmp."""
    root = tmp_path / "old"
    for addr in ("cursor", "owner"):
        for sub in ("cur", "new", "tmp"):
            (root / addr / sub).mkdir(parents=True)
    (root / "addresses.json").write_text("{}")
    (root / "cursor" / "cur" / "1.M1.host:2,RSTn").write_bytes(_mail("<a@x>"))
    (root / "cursor" / "cur" / "2.M2.host:2,").write_bytes(_mail("<b@x>"))
    (root / "cursor" / "new" / "3.M3.host").write_bytes(_mail("<c@x>"))
    (root / "owner" / "cur" / "4.M4.host:2,S").write_bytes(_mail("<d@x>"))
    return root


def _snapshot(root: Path):
    return sorted(
        (str(p.relative_to(root)), p.stat().st_mtime_ns, p.read_bytes())
        for p in root.rglob("*")
        if p.is_file()
    )


class MemoryAccounts(AccountDirectory):
    """The accounts of a test. The owner and the agent cursor exist at the start."""

    def __init__(self) -> None:
        self.names = {"owner", "board", "cursor"}
        self.added: list[str] = []

    async def exists(self, account: str) -> bool:
        return account in self.names

    async def add_agent(self, name: str) -> None:
        if not name.islower():
            raise ValueError(f"bad agent name: {name}")
        self.names.add(name)
        self.added.append(name)


@pytest.fixture
def accounts():
    return MemoryAccounts()


@pytest.fixture
def parts(tmp_path, accounts):
    clock = FixedClock(dt.datetime(2026, 11, 12, 2, 0, tzinfo=dt.UTC))
    paths = OpsPaths(
        backup_dir=tmp_path / "backup", mail_dir=tmp_path / "data" / "mail", keep=3
    )
    tool, importer = MemoryBackupTool(), MemoryMailImporter()
    return (
        OpsService(paths, tool, FsMaildirSource(), importer, clock, accounts),
        clock,
        paths,
        tool,
        importer,
    )


def test_import_maildir_skips_copies_and_keeps_the_source(tmp_path, parts):
    ops, _, _, _, importer = parts
    root = make_old_maildir(tmp_path)
    before = _snapshot(root)
    result = anyio.run(ops.import_maildir, root)
    assert result["total"] == 4
    assert result["accounts"]["cursor"] == {"imported": 3, "skipped": 0, "failed": 0}
    assert result["accounts"]["owner"]["imported"] == 1
    flags = {m[2]: m[1] for m in importer.mails["cursor"]}
    assert flags["<a@x>"] == ["\\Answered", "\\Seen", "$MaiNotified"]
    again = anyio.run(ops.import_maildir, root)
    assert again["accounts"]["cursor"]["skipped"] == 3
    assert _snapshot(root) == before


def _add_folder(root: Path, name: str, mid: str) -> None:
    (root / name / "cur").mkdir(parents=True)
    (root / name / "cur" / "9.M9.host:2,").write_bytes(_mail(mid))


def test_the_mail_of_an_unknown_account_is_an_error_without_add_agents(
    tmp_path, parts, accounts
):
    ops, _, _, _, importer = parts
    root = make_old_maildir(tmp_path)
    _add_folder(root, "reviewer", "<r@x>")
    result = anyio.run(ops.import_maildir, root)
    assert result["accounts"]["reviewer"] == {"imported": 0, "skipped": 0, "failed": 1}
    assert [e["error"] for e in result["errors"]] == ["UNKNOWN_ACCOUNT"]
    assert "reviewer" not in importer.mails and accounts.added == []


def test_add_agents_makes_the_agent_of_each_new_folder(tmp_path, parts, accounts):
    ops, _, _, _, importer = parts
    root = make_old_maildir(tmp_path)
    _add_folder(root, "reviewer", "<r@x>")
    _add_folder(root, "Bad", "<b2@x>")
    result = anyio.run(lambda: ops.import_maildir(root, add_agents=True))
    assert accounts.added == ["reviewer"]
    assert result["agents_added"] == ["reviewer"]
    assert result["accounts"]["reviewer"]["imported"] == 1
    assert [e["error"] for e in result["errors"]] == ["BAD_AGENT_NAME"]
    assert [m[2] for m in importer.mails["reviewer"]] == ["<r@x>"]


def test_the_import_renames_the_header_prefixes_of_each_flag(tmp_path, parts):
    ops, _, _, _, importer = parts
    root = tmp_path / "old"
    (root / "cursor" / "cur").mkdir(parents=True)
    (root / "cursor" / "cur" / "1.M1.host:2,").write_bytes(
        b"Message-ID: <h@x>\r\nX-Old-Kind: request\r\n\r\nbody\r\n"
    )
    anyio.run(lambda: ops.import_maildir(root, rename_header_prefix=["X-Old-=X-Mai-"]))
    raw = importer.mails["cursor"][0][0]
    assert b"X-Mai-Kind: request" in raw and b"X-Old" not in raw


def test_import_dry_run_writes_nothing(tmp_path, parts):
    ops, _, _, _, importer = parts
    result = anyio.run(
        lambda: ops.import_maildir(make_old_maildir(tmp_path), dry_run=True)
    )
    assert result["dry_run"] and result["total"] == 4
    assert importer.mails == {}


def test_backup_restore_round_trip_and_retention(parts):
    ops, clock, paths, tool, _ = parts
    paths.mail_dir.mkdir(parents=True)
    (paths.mail_dir / "a.txt").write_text("first")
    first = anyio.run(ops.backup)
    assert Path(first["dump"]).exists() and Path(first["mail"]).exists()
    assert oct(Path(first["dump"]).stat().st_mode & 0o777) == "0o600"
    (paths.mail_dir / "a.txt").write_text("changed")
    (paths.mail_dir / "b.txt").write_text("new")
    restored = anyio.run(ops.restore, first["stamp"])
    assert restored["dump"] == first["dump"]
    assert (paths.mail_dir / "a.txt").read_text() == "first"
    assert not (paths.mail_dir / "b.txt").exists()
    assert tool.daemon_calls == ["stop", "start"]
    for _ in range(4):
        clock.advance(dt.timedelta(days=1))
        last = anyio.run(ops.backup)
    assert len(ops.list_backups()) == 3
    assert first["stamp"] in last["removed"] or first["stamp"] not in ops.list_backups()


def test_restore_of_a_missing_backup_fails(parts):
    ops = parts[0]
    with pytest.raises(FileNotFoundError):
        anyio.run(ops.restore, "20200101T000000Z")

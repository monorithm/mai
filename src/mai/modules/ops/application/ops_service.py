"""Backup, restore, and import of a 0.5.0 Maildir.

A backup writes two parts with one time stamp into the backup directory: a
database dump and a tar file of the mail directory. The newest 14 backups
are kept. The import appends each mail of a 0.5.0 Maildir root to the queue
of the account with the same name as its folder, and never changes the source.
"""

from __future__ import annotations

import logging
import os
import shutil
import tarfile
from pathlib import Path

import anyio.to_thread
from bustan import Injectable

from mai.platform.clock import Clock

from ..domain.backups import (
    backup_prefix,
    is_stamp,
    stamp_of,
    stamps_in,
    stamps_to_remove,
)
from ..domain.maildir import HeaderRenamer, parse_header_rename
from ..domain.ports import AccountDirectory, BackupTool, MaildirSource, MailImporter
from .ops_paths import OpsPaths

log = logging.getLogger("mai.ops")


def _tar_mail(mail_dir: Path, target: Path) -> None:
    """Write the tar of the mail directory, while Dovecot runs.

    Dovecot and the router move mail files during the walk. A file or a
    folder that is not there when the walk comes to it is not in the tar: a
    mail that moves during the backup can be missing from it. The tar has the
    name ".partial" until it is complete, thus a stopped backup leaves no file
    that looks like a backup.
    """
    partial = target.with_name(target.name + ".partial")
    with tarfile.open(partial, "w:gz") as tf:
        if mail_dir.exists():
            tf.add(str(mail_dir), arcname="mail", recursive=False)
            for root, dirs, files in os.walk(mail_dir):
                dirs.sort()
                for name in [*dirs, *sorted(files)]:
                    path = Path(root) / name
                    arcname = "mail/" + str(path.relative_to(mail_dir))
                    try:
                        tf.add(str(path), arcname=arcname, recursive=False)
                    except FileNotFoundError:
                        continue
    partial.replace(target)


def _prepare_mail(source: Path, mail_dir: Path) -> Path:
    """Extract the tar to a new work directory, next to the mail directory. Give the extracted directory.

    This step changes no live file. An error removes the work directory.
    A copy of the live mail from an earlier restore that stopped half way
    goes back first, when the live directory does not exist.
    """
    work = mail_dir.with_name(mail_dir.name + ".restore-work")
    old = mail_dir.with_name(mail_dir.name + ".before-restore")
    if old.exists() and not mail_dir.exists():
        # The only copy of the live mail: an earlier restore stopped between its two renames.
        old.rename(mail_dir)
    if work.exists():
        shutil.rmtree(work)
    work.mkdir(parents=True)
    try:
        with tarfile.open(source, "r:gz") as tf:
            tf.extractall(work, filter="tar")
    except BaseException:
        shutil.rmtree(work, ignore_errors=True)
        raise
    extracted = work / "mail"
    if not extracted.exists():
        extracted.mkdir()
    return extracted


def _swap_mail(extracted: Path, mail_dir: Path) -> None:
    """Replace the live mail directory with the extracted one."""
    work = extracted.parent
    old = mail_dir.with_name(mail_dir.name + ".before-restore")
    if old.exists() and mail_dir.exists():
        shutil.rmtree(old)
    if mail_dir.exists():
        mail_dir.rename(old)
    try:
        extracted.rename(mail_dir)
    except BaseException:
        if old.exists() and not mail_dir.exists():
            old.rename(mail_dir)
        raise
    shutil.rmtree(work, ignore_errors=True)
    if old.exists():
        shutil.rmtree(old)
    _fix_mail_owner(mail_dir)


def _discard_prepared(extracted: Path) -> None:
    shutil.rmtree(extracted.parent, ignore_errors=True)


def _untar_mail(source: Path, mail_dir: Path) -> None:
    """Replace the mail directory with the one of the tar. An error leaves the live directory as it was."""
    _swap_mail(_prepare_mail(source, mail_dir), mail_dir)


def _fix_mail_owner(mail_dir: Path) -> None:
    """Give the mail files to the vmail user when that user exists."""
    try:
        import pwd

        pw = pwd.getpwnam("vmail")
    except (KeyError, ImportError):
        return
    for root, dirs, files in os.walk(mail_dir):
        for n in [*dirs, *files, "."]:
            try:
                os.chown(os.path.join(root, n), pw.pw_uid, pw.pw_gid)
            except OSError:
                pass


@Injectable()
class OpsService:
    def __init__(
        self,
        paths: OpsPaths,
        tool: BackupTool,
        source: MaildirSource,
        importer: MailImporter,
        clock: Clock,
        accounts: AccountDirectory,
    ) -> None:
        self._paths = paths
        self._tool = tool
        self._source = source
        self._importer = importer
        self._clock = clock
        self._accounts = accounts

    # ------------------------------------------------------------ backup

    def list_backups(self, backup_dir: Path | None = None) -> list[str]:
        bdir = Path(backup_dir or self._paths.backup_dir)
        if not bdir.is_dir():
            return []
        return stamps_in([f.name for f in bdir.iterdir()])

    def prune(
        self, backup_dir: Path | None = None, keep: int | None = None
    ) -> list[str]:
        """Remove all but the newest `keep` backups. Return the removed stamps."""
        bdir = Path(backup_dir or self._paths.backup_dir)
        removed = stamps_to_remove(
            self.list_backups(bdir), self._paths.keep if keep is None else keep
        )
        for s in removed:
            for suffix in (".pgdump", ".mail.tar.gz"):
                (bdir / f"mai-{s}{suffix}").unlink(missing_ok=True)
        return removed

    async def backup(
        self,
        backup_dir: str | os.PathLike | None = None,
        mail_dir: str | os.PathLike | None = None,
        keep: int | None = None,
    ) -> dict:
        """Write a database dump and a mail tar with one time stamp."""
        bdir = Path(backup_dir or self._paths.backup_dir)
        mdir = Path(mail_dir or self._paths.mail_dir)
        bdir.mkdir(parents=True, exist_ok=True)
        with self._tool.exclusive():
            stamp = stamp_of(self._clock.now())
            dump = bdir / f"mai-{stamp}.pgdump"
            tarp = bdir / f"mai-{stamp}.mail.tar.gz"
            partial_dump = dump.with_name(dump.name + ".partial")
            try:
                await self._tool.dump_database(partial_dump)
                await anyio.to_thread.run_sync(_tar_mail, mdir, tarp)
                partial_dump.replace(dump)
            except BaseException:
                # A failed backup leaves no part: a partial file is never a backup.
                for leftover in (
                    partial_dump,
                    tarp.with_name(tarp.name + ".partial"),
                    tarp,
                ):
                    leftover.unlink(missing_ok=True)
                raise
            os.chmod(dump, 0o600)
            os.chmod(tarp, 0o600)
            removed = self.prune(bdir, keep)
        return {
            "stamp": stamp,
            "dump": str(dump),
            "mail": str(tarp),
            "removed": removed,
        }

    # ----------------------------------------------------------- restore

    async def restore(
        self, path: str | os.PathLike, mail_dir: str | os.PathLike | None = None
    ) -> dict:
        """Restore one backup.

        `path` is a time stamp, one of the two files of a backup, or their
        common prefix. The daemons stop, the database and the mail directory
        are restored, and the daemons start again.
        """
        p = Path(path)
        if p.parent == Path(".") and not p.exists() and is_stamp(p.name):
            p = self._paths.backup_dir / f"mai-{p.name}"
        prefix = backup_prefix(str(p))
        dump, tarp = Path(prefix + ".pgdump"), Path(prefix + ".mail.tar.gz")
        if not dump.exists() or not tarp.exists():
            raise FileNotFoundError(f"backup parts not found for {prefix}")
        mdir = Path(mail_dir or self._paths.mail_dir)
        await self._tool.stop_daemons()
        failed = False
        try:
            # The tar is extracted first: a bad tar stops the restore before the database changes.
            extracted = await anyio.to_thread.run_sync(_prepare_mail, tarp, mdir)
            try:
                await self._tool.restore_database(dump)
            except BaseException:
                await anyio.to_thread.run_sync(_discard_prepared, extracted)
                raise
            await anyio.to_thread.run_sync(_swap_mail, extracted, mdir)
        except BaseException:
            failed = True
            raise
        finally:
            try:
                await self._tool.start_daemons()
            except Exception as start_error:
                if not failed:
                    raise
                # The error of the restore is the one to show: keep it, and log this one.
                log.error(
                    "after the failed restore, the daemons did not start again: %s",
                    start_error,
                )
        return {"restored": prefix, "dump": str(dump), "mail": str(tarp)}

    # ------------------------------------------------------------ import

    async def import_maildir(
        self,
        path: str | os.PathLike,
        dry_run: bool = False,
        add_agents: bool = False,
        rename_header_prefix: list[str] | None = None,
    ) -> dict:
        """Append each mail of a 0.5.0 Maildir to the queue of the account of its folder.

        The account has the name of the folder. An account that does not exist
        is an error of each of its mails, unless add_agents is True: then the
        import makes the agent account first. Each "OLD=NEW" value of
        rename_header_prefix changes the header names that start with OLD to
        start with NEW. A mail whose Message-ID is already in the target INBOX
        is skipped, so a second run makes no copies. The source is never
        changed.
        """
        rename = HeaderRenamer(
            [parse_header_rename(v) for v in rename_header_prefix or []]
        )
        root = Path(path).expanduser()
        mails = await self._source.scan(root)
        counts: dict[str, dict[str, int]] = {}
        errors: list[dict] = []
        ready: dict[str, str | None] = {}
        added: list[str] = []
        for m in mails:
            c = counts.setdefault(m.account, {"imported": 0, "skipped": 0, "failed": 0})
            if m.account not in ready:
                ready[m.account] = await self._account_ready(
                    m.account, add_agents, dry_run, added
                )
            problem = ready[m.account]
            if problem is not None:
                c["failed"] += 1
                errors.append(
                    {"file": str(m.path), "account": m.account, "error": problem}
                )
                continue
            if dry_run:
                c["imported"] += 1
                continue
            try:
                raw = rename(await self._source.read(m))
                c[
                    await self._importer.append(m.account, raw, m.flags, m.message_id)
                ] += 1
            except Exception as exc:  # noqa: BLE001 - report the mail and continue with the next one
                c["failed"] += 1
                errors.append(
                    {
                        "file": str(m.path),
                        "account": m.account,
                        "error": type(exc).__name__,
                    }
                )
        return {
            "source": str(root),
            "total": len(mails),
            "accounts": counts,
            "agents_added": added,
            "errors": errors,
            "dry_run": dry_run,
        }

    async def _account_ready(
        self, account: str, add_agents: bool, dry_run: bool, added: list[str]
    ) -> str | None:
        """None when the account can get mail, else the error code of its mails."""
        if await self._accounts.exists(account):
            return None
        if not add_agents:
            return "UNKNOWN_ACCOUNT"
        if not dry_run:
            try:
                await self._accounts.add_agent(account)
            except ValueError:
                return "BAD_AGENT_NAME"
        added.append(account)
        return None

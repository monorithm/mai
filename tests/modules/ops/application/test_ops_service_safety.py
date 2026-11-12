"""The safety rules of backup and restore: no partial files, no lost mail directory, the real error first."""

import tarfile

import anyio
import pytest

from mai.modules.ops.application import ops_service
from mai.modules.ops.application.ops_paths import OpsPaths
from mai.modules.ops.application.ops_service import _tar_mail, _untar_mail
from mai.modules.ops.domain.ports import BackupTool


def _maildir(root):
    (root / "cursor" / "cur").mkdir(parents=True)
    (root / "cursor" / "cur" / "1.mail").write_text("one")
    (root / "cursor" / "new").mkdir()
    (root / "cursor" / "new" / "2.mail").write_text("two")
    return root


def test_a_file_that_moves_during_the_tar_is_left_out_and_the_tar_is_complete(
    tmp_path, monkeypatch
):
    mail = _maildir(tmp_path / "mail")
    target = tmp_path / "b.mail.tar.gz"
    real_add = tarfile.TarFile.add

    def add(self, name, *args, **kwargs):
        if name.endswith("2.mail"):
            raise FileNotFoundError(name)  # Dovecot moved it from new/ to cur/
        return real_add(self, name, *args, **kwargs)

    monkeypatch.setattr(tarfile.TarFile, "add", add)
    _tar_mail(mail, target)
    with tarfile.open(target) as tf:
        names = tf.getnames()
    assert "mail/cursor/cur/1.mail" in names and "mail/cursor/new/2.mail" not in names
    assert not (tmp_path / "b.mail.tar.gz.partial").exists()


def test_a_stopped_tar_leaves_only_a_partial_file(tmp_path, monkeypatch):
    mail = _maildir(tmp_path / "mail")
    target = tmp_path / "b.mail.tar.gz"

    def add(self, name, *args, **kwargs):
        raise OSError("disk full")

    monkeypatch.setattr(tarfile.TarFile, "add", add)
    with pytest.raises(OSError):
        _tar_mail(mail, target)
    assert not target.exists()


def test_a_bad_tar_leaves_the_live_mail_directory_as_it_was(tmp_path):
    mail = _maildir(tmp_path / "mail")
    bad = tmp_path / "bad.mail.tar.gz"
    bad.write_bytes(b"not a tar")
    with pytest.raises(tarfile.TarError):
        _untar_mail(bad, mail)
    assert (mail / "cursor" / "cur" / "1.mail").read_text() == "one"
    assert not (tmp_path / "mail.restore-work").exists()


def test_a_good_tar_replaces_the_mail_directory(tmp_path, monkeypatch):
    monkeypatch.setattr(ops_service, "_fix_mail_owner", lambda d: None)
    mail = _maildir(tmp_path / "mail")
    target = tmp_path / "b.mail.tar.gz"
    _tar_mail(mail, target)
    (mail / "cursor" / "cur" / "1.mail").write_text("changed after the backup")
    _untar_mail(target, mail)
    assert (mail / "cursor" / "cur" / "1.mail").read_text() == "one"
    assert not (tmp_path / "mail.before-restore").exists()


class _Tool(BackupTool):
    # The default exclusive() of BackupTool gives a context with no lock.

    async def dump_database(self, target):
        raise AssertionError("a restore does not dump the database")

    async def restore_database(self, source):
        raise RuntimeError("pg_restore stopped")

    async def stop_daemons(self):
        pass

    async def start_daemons(self):
        raise RuntimeError("the controller did not start the daemons again")


def test_the_error_of_the_restore_is_not_hidden_by_the_start_error(tmp_path, caplog):
    stamp = "20261112T020000Z"
    (tmp_path / f"mai-{stamp}.pgdump").write_bytes(b"x")
    live = _maildir(tmp_path / "mail")
    _tar_mail(live, tmp_path / f"mai-{stamp}.mail.tar.gz")
    service = ops_service.OpsService.__new__(ops_service.OpsService)
    service._tool = _Tool()
    service._paths = OpsPaths(backup_dir=tmp_path, mail_dir=tmp_path / "mail")
    with pytest.raises(RuntimeError, match="pg_restore stopped"):
        anyio.run(service.restore, str(tmp_path / f"mai-{stamp}"))
    assert any("did not start again" in r.getMessage() for r in caplog.records)
    # The database part failed: the live mail stays, and no work directory stays.
    assert (live / "cursor" / "cur" / "1.mail").read_text() == "one"
    assert not (tmp_path / "mail.restore-work").exists()


def test_a_bad_tar_stops_the_restore_before_the_database_changes(tmp_path):
    stamp = "20261112T020000Z"
    (tmp_path / f"mai-{stamp}.pgdump").write_bytes(b"x")
    (tmp_path / f"mai-{stamp}.mail.tar.gz").write_bytes(b"not a tar")
    calls = []

    class Tool(_Tool):
        async def restore_database(self, source):
            calls.append("restore_database")

        async def start_daemons(self):
            calls.append("start")

    service = ops_service.OpsService.__new__(ops_service.OpsService)
    service._tool = Tool()
    service._paths = OpsPaths(backup_dir=tmp_path, mail_dir=tmp_path / "mail")
    with pytest.raises(tarfile.TarError):
        anyio.run(service.restore, str(tmp_path / f"mai-{stamp}"))
    assert calls == ["start"]


def test_a_copy_of_the_live_mail_from_a_stopped_restore_comes_back(
    tmp_path, monkeypatch
):
    monkeypatch.setattr(ops_service, "_fix_mail_owner", lambda d: None)
    mail = _maildir(tmp_path / "src")
    target = tmp_path / "b.mail.tar.gz"
    _tar_mail(mail, target)
    live = tmp_path / "mail"
    old = tmp_path / "mail.before-restore"
    _maildir(old)  # an earlier restore stopped after the first rename
    extracted = ops_service._prepare_mail(target, live)
    assert (live / "cursor" / "cur" / "1.mail").exists()
    ops_service._discard_prepared(extracted)

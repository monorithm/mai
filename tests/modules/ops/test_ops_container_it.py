"""Container tests of backup, restore and the 0.5.0 Maildir import.

They run against a running mai:dev container, named by
MAI_IT_CONTAINER (default mai-test-uiops), and are skipped
unless MAI_IT is set.
"""

import json
import os
import subprocess
import uuid

import pytest

from .application.test_ops_service import make_old_maildir

pytestmark = pytest.mark.skipif(
    not os.environ.get("MAI_IT"), reason="needs the mai:dev container"
)

CONTAINER = os.environ.get("MAI_IT_CONTAINER", "mai-test-uiops")


def _exec(*args: str, check: bool = True) -> str:
    r = subprocess.run(
        ["docker", "exec", CONTAINER, *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if check:
        assert r.returncode == 0, r.stderr[-2000:]
    return r.stdout


def _json_of(out: str) -> dict:
    return json.loads(out[out.index("{") :])


def _psql(sql: str) -> str:
    return _exec(
        "psql", "-h", "/run/postgresql", "-U", "postgres", "-d", "mai", "-tAc", sql
    ).strip()


def test_backup_then_restore_round_trip():
    marker = f"it_{uuid.uuid4().hex[:8]}"
    # pg_restore --clean drops only the objects in the dump, so the marker
    # table is made before the backup and dropped after it: a good restore
    # brings it back.
    _psql(f"CREATE TABLE public.{marker} (x int)")
    backup = _json_of(_exec("mai", "backup"))
    stamp = backup["stamp"]
    _psql(f"DROP TABLE public.{marker}")
    _exec("sh", "-c", f"echo x > /data/mail/{marker}")
    restored = _json_of(_exec("mai", "restore", stamp))
    assert restored["dump"].endswith(f"mai-{stamp}.pgdump")
    assert _psql(f"SELECT to_regclass('public.{marker}') IS NOT NULL") == "t"
    assert (
        _exec("sh", "-c", f"test -e /data/mail/{marker} && echo yes || echo no").strip()
        == "no"
    )
    assert _psql("SELECT count(*) > 0 FROM mail_auth.principal") == "t"


def test_import_maildir_of_a_temp_old_maildir(tmp_path):
    root = make_old_maildir(tmp_path)
    target = f"/data/old-{uuid.uuid4().hex[:8]}"
    subprocess.run(["docker", "cp", str(root), f"{CONTAINER}:{target}"], check=True)
    try:
        first = _json_of(_exec("mai", "import-maildir", "--add-agents", target))
        assert first["total"] == 4
        assert first["errors"] == []
        assert (
            first["accounts"]["cursor"]["imported"]
            + first["accounts"]["cursor"]["skipped"]
            == 3
        )
        again = _json_of(_exec("mai", "import-maildir", target))
        assert again["accounts"]["cursor"]["skipped"] == 3
        assert again["accounts"]["owner"]["skipped"] == 1
    finally:
        _exec("rm", "-rf", target, check=False)

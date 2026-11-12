"""The command "mai owner set-key": one line on stdin, exit code 3 for a key that exists."""

import io

from mai import app_cli
from mai.modules.credentials.domain.errors import AccountStateError

KEY = "mai1:owner:" + "a" * 52


def test_owner_set_key_reads_one_line_and_gives_exit_code_3_for_a_key_that_exists(
    monkeypatch, capsys
):
    seen = []

    def fake_call(token_path, method, *args, **kwargs):
        seen.append((method, args, kwargs))
        if len(seen) > 1 and not kwargs.get("replace"):
            raise AccountStateError("an owner key exists")
        return {"owner_key": "set"}

    monkeypatch.setattr(app_cli, "_call", fake_call)
    monkeypatch.setattr("sys.stdin", io.StringIO(KEY + "\n"))
    assert app_cli.main(["owner", "set-key"]) == 0
    monkeypatch.setattr("sys.stdin", io.StringIO(KEY + "\n"))
    assert app_cli.main(["owner", "set-key"]) == 3
    monkeypatch.setattr("sys.stdin", io.StringIO(KEY + "\n"))
    assert app_cli.main(["owner", "set-key", "--replace"]) == 0
    assert seen[0] == ("owner_set_key", (KEY,), {"replace": False})
    out = capsys.readouterr()
    assert KEY not in out.out and KEY not in out.err

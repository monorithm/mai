"""The mai command line: the arguments that each command gives to its service."""

import pytest

from mai import app_cli


@pytest.fixture
def calls(monkeypatch):
    """Record each service call of the CLI and give a fixed result."""
    recorded: list[tuple] = []
    results: dict[str, object] = {}

    def fake_call(token_path, method, *args, **kwargs):
        recorded.append((token_path.rsplit(".", 1)[1], method, args, kwargs))
        result = results.get(method)
        return result(*args) if callable(result) else result

    monkeypatch.setattr(app_cli, "_call", fake_call)
    recorded_results = results
    return recorded, recorded_results


def test_agent_add_adds_each_name(calls, capsys):
    recorded, results = calls
    results["agent_add"] = lambda name: {
        "agent": name,
        "address": f"{name}@mai.internal",
        "created": name != "api",
        "acl_set": True,
    }
    assert app_cli.main(["agent", "add", "designer", "api"]) == 0
    assert [(c[1], c[2]) for c in recorded] == [
        ("agent_add", ("designer",)),
        ("agent_add", ("api",)),
    ]
    out = capsys.readouterr().out
    assert "added:      designer@mai.internal" in out
    assert "exists:     api@mai.internal" in out


def test_agent_list_shows_the_agents(calls, capsys):
    _recorded, results = calls
    results["agent_list"] = [
        {"agent": "engineer", "status": "active", "sessions": 2},
    ]
    assert app_cli.main(["agent", "list"]) == 0
    assert "engineer" in capsys.readouterr().out
    assert app_cli.main(["agent", "list", "--json"]) == 0
    assert '"sessions": 2' in capsys.readouterr().out


def test_a_bad_agent_name_gives_exit_code_1(monkeypatch, capsys):
    def refuse(*_args, **_kwargs):
        raise ValueError("agent name must match [a-z0-9][a-z0-9-]{0,47}: 'Bad'")

    monkeypatch.setattr(app_cli, "_call", refuse)
    assert app_cli.main(["agent", "add", "Bad"]) == 1
    assert "agent name must match" in capsys.readouterr().err


@pytest.mark.parametrize(
    "fmt", ["plain", "claude", "cursor", "copilot", "codex", "antigravity"]
)
def test_hook_gives_the_format_and_the_event_to_the_watch_service(calls, fmt):
    recorded, _results = calls
    argv = ["hook", "--credential-file", "-", "--format", fmt, "--event", "Stop"]
    assert app_cli.main(argv) == 0
    assert recorded == [("WatchService", "hook", ("-",), {"fmt": fmt, "event": "Stop"})]


def test_hook_defaults_to_the_plain_format_and_no_event(calls):
    recorded, _results = calls
    assert app_cli.main(["hook", "--credential-file", "/c.cred"]) == 0
    assert recorded[0][3] == {"fmt": "plain", "event": None}


def test_hook_refuses_an_unknown_format():
    with pytest.raises(SystemExit):
        app_cli.build_parser().parse_args(
            ["hook", "--credential-file", "-", "--format", "other"]
        )


def test_import_maildir_gives_the_agent_and_header_flags(calls):
    recorded, _results = calls
    argv = [
        "import-maildir",
        "/old",
        "--add-agents",
        "--rename-header-prefix",
        "X-Old-=X-Mai-",
        "--rename-header-prefix",
        "X-Other-=X-Mai-",
    ]
    assert app_cli.main(argv) == 0
    assert recorded == [
        (
            "OpsService",
            "import_maildir",
            ("/old",),
            {
                "dry_run": False,
                "add_agents": True,
                "rename_header_prefix": ["X-Old-=X-Mai-", "X-Other-=X-Mai-"],
            },
        )
    ]


def test_project_seed_has_no_default_agents():
    with pytest.raises(SystemExit):
        app_cli.build_parser().parse_args(["project", "seed", "p"])
    args = app_cli.build_parser().parse_args(
        ["project", "seed", "p", "--supervisor", "architect", "--worker", "engineer"]
    )
    assert (args.supervisor, args.worker, args.lane) == ("architect", "engineer", None)

import random

from mai.modules.credentials.domain.credential import (
    COPIES_LOGIN,
    SYSTEM_ACCOUNTS,
    check_agent_name,
    check_label,
)
from mai.modules.mcp.application.json_rpc import validate
from mai.modules.mcp.domain import tool_schemas as schemas
from mai.platform.contract import ToolError


def test_twenty_two_tools_with_schemas_and_annotations():
    assert len(schemas.TOOLS) == 22
    for t in schemas.TOOLS:
        assert (
            t["inputSchema"]["type"] == "object"
            and t["outputSchema"]["type"] == "object"
        )
        assert (
            t["annotations"]["openWorldHint"] is False
            and t["annotations"]["destructiveHint"] is False
        )
    assert schemas.BY_NAME["list_inbox"]["annotations"]["readOnlyHint"] is True
    assert schemas.BY_NAME["send_message"]["annotations"]["idempotentHint"] is True


def test_login_takes_a_name_and_each_other_tool_an_optional_session():
    login = schemas.BY_NAME["login"]["inputSchema"]
    assert login["required"] == ["session"]
    assert login["properties"]["session"]["type"] == "string"
    assert login["additionalProperties"] is False
    assert schemas.BY_NAME["login"]["outputSchema"]["anyOf"] == [
        {"required": ["session", "login", "agent"]},
        {"required": ["error"]},
    ]
    logout = schemas.BY_NAME["logout"]["inputSchema"]
    assert logout["required"] == ["session"]
    assert logout["properties"]["session"]["pattern"] == SESSION_PATTERN
    for t in schemas.TOOLS:
        if t["name"] in ("login", "logout"):
            continue
        session = t["inputSchema"]["properties"]["session"]
        assert session["type"] == "string", t["name"]
        assert session["pattern"] == SESSION_PATTERN, t["name"]
        assert "lane-1" in session["description"], t["name"]
        assert "session" not in t["inputSchema"].get("required", ()), t["name"]


SESSION_PATTERN = (
    r"^(ses-[a-z2-7]{16}|([a-z0-9][a-z0-9-]{0,47}\.)?[a-z0-9][a-z0-9-]{0,31})$"
)
SYSTEM_NAMES = {login for login, _kind in SYSTEM_ACCOUNTS} | {COPIES_LOGIN}


def _agrees(check, value):
    try:
        check(value)
    except ValueError:
        return False
    return True


def _named_form(value):
    """True when VALUE is a label or AGENT.LABEL by the rules of the credentials module.

    The names of the system accounts have the form of an agent name, but no agent
    has them. The server refuses a session of such a name.
    """
    agent, dot, label = value.partition(".")
    if not dot:
        return _agrees(check_label, value)
    agent_ok = _agrees(check_agent_name, agent) or agent in SYSTEM_NAMES
    return agent_ok and _agrees(check_label, label)


def _accepts(value):
    try:
        validate(schemas.SESSION_ARG, value)
    except ToolError as exc:
        assert exc.code == "INVALID_ARGUMENT"
        return False
    return True


EDGES = [
    "",
    "a",
    "lane-1",
    "lane-a",
    "s",
    "s1",
    "s01",
    "s1a",
    "cursor.s1",
    "cursor.lane-1",
    "owner.lane-1",
    "ses-abcdefghijklmnop",
    "ses-abcdefghijklmno",
    "ses-ABCDEFGHIJKLMNOP",
    "cursor.ses-abcdefghijklmnop",
    "l" * 32,
    "l" * 33,
    "a" * 48 + ".b",
    "a" * 49 + ".b",
    "a" * 48 + "." + "b" * 32,
    "a" * 48 + "." + "b" * 33,
    "-lane",
    "lane-",
    "Lane-1",
    "lane_1",
    "lane 1",
    "lane-1\n",
    "lane-1\r",
    "\nlane-1",
    "cursor.lane-1\n",
    "cursor.",
    ".lane-1",
    "cursor..lane-1",
    "a.b.c",
    "cursor@mai.internal",
    "cursor.lane-1@mai.internal",
    "c\u00e9.lane",
]


def test_the_session_pattern_agrees_with_the_label_rule_and_the_agent_name_rule():
    pattern = schemas.SESSION_ARG["pattern"]
    assert pattern == SESSION_PATTERN
    rng = random.Random(320)
    alphabet = "aes0179-._Z \n"
    values = list(EDGES)
    for _ in range(20000):
        values.append("".join(rng.choices(alphabet, k=rng.randint(0, 40))))
    for _ in range(5000):
        agent = "".join(rng.choices("ac9-", k=rng.randint(0, 50)))
        label = "".join(rng.choices("s19-a", k=rng.randint(0, 34)))
        values.append(f"{agent}.{label}")
    for value in values:
        assert _accepts(value) == _named_form(value), repr(value)


def test_the_session_pattern_accepts_a_handle_a_label_a_login_and_a_pool_label():
    for value in ("ses-abcdefghijklmnop", "lane-1", "cursor.lane-1", "s1", "cursor.s1"):
        assert _accepts(value), value
    for value in ("lane-1\n", "Lane-1", "a.b.c", "cursor.lane-1@mai.internal", 3, None):
        assert not _accepts(value), value

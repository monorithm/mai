from mai.modules.mcp.domain import tool_schemas as schemas


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


def test_login_and_logout_take_a_session_and_each_other_tool_an_optional_handle():
    login = schemas.BY_NAME["login"]["inputSchema"]
    assert login["required"] == ["session"]
    assert login["properties"]["session"]["type"] == "string"
    assert login["additionalProperties"] is False
    assert schemas.BY_NAME["login"]["outputSchema"]["required"] == [
        "session",
        "login",
        "agent",
    ]
    logout = schemas.BY_NAME["logout"]["inputSchema"]
    assert logout["required"] == ["session"]
    assert logout["properties"]["session"]["pattern"] == r"^ses-[a-z2-7]{16}$"
    for t in schemas.TOOLS:
        if t["name"] in ("login", "logout"):
            continue
        session = t["inputSchema"]["properties"]["session"]
        assert session["type"] == "string", t["name"]
        assert session["pattern"] == r"^ses-[a-z2-7]{16}$", t["name"]
        assert "session" not in t["inputSchema"].get("required", ()), t["name"]

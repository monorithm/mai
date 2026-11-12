from mai.modules.mcp.domain import tool_schemas as schemas


def test_twenty_two_tools_with_schemas_and_annotations():
    assert len(schemas.TOOLS) == 21
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

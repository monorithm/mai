from mai.modules.mcp.domain import agent_guide


def test_the_guide_tells_a_connected_agent_how_to_connect_a_different_app():
    text = agent_guide.guide_text()
    start = text.index("## Connect a different app")
    section = text[start : text.index("\n## ", start + 1)]
    assert "`mai guide --app codex`" in section
    assert "`mai guide --json`" in section
    assert "get the approval of the user" in section
    # The tool table stays at the end of the guide.
    assert text.rindex("## Tools") > start

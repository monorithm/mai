from mai.modules.mcp.domain import agent_guide


def test_the_guide_tells_a_connected_agent_how_to_connect_a_different_app():
    text = agent_guide.guide_text()
    start = text.index("## Connect a different app")
    section = text[start : text.index("\n## ", start + 1)]
    assert "`mai guide --app codex`" in section
    assert "`mai guide --json`" in section
    assert "get the approval of the user" in section
    # A rerun with no --app gives the steps of the app of the agent, not of the other app.
    assert "run the same\n   command again" in section
    assert "the user does the check there" in section
    # The tool table stays at the end of the guide.
    assert text.rindex("## Tools") > start

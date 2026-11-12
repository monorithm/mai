from mai.platform.contract import BACKGROUND_ENV, background_on


def test_background_work_is_off_unless_the_server_turns_it_on(monkeypatch):
    monkeypatch.delenv(BACKGROUND_ENV, raising=False)
    assert background_on() is False
    monkeypatch.setenv(BACKGROUND_ENV, "on")
    assert background_on() is True

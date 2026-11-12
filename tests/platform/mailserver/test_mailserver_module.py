import asyncio

from bustan.testing import create_testing_module

from mai.platform.mailserver import (
    MailClientFactory,
    MailServerModule,
    MailSession,
)


def test_the_module_gives_one_client_factory() -> None:
    async def scenario() -> None:
        compiled = await create_testing_module(MailServerModule).compile()
        try:
            assert isinstance(compiled.get(MailClientFactory), MailClientFactory)
        finally:
            await compiled.close()

    asyncio.run(scenario())


def test_the_factory_opens_a_session_with_its_hosts_and_does_not_connect() -> None:
    factory = MailClientFactory(
        imap_host="h1", imap_port=11, smtp_host="h2", smtp_port=22, timeout=3
    )
    session = factory.open("cursor.lane-a", "not-shown")
    assert isinstance(session, MailSession)
    assert (session.login, session.imap_host, session.imap_port) == (
        "cursor.lane-a",
        "h1",
        11,
    )
    assert (session.smtp_host, session.smtp_port, session.timeout) == ("h2", 22, 3)
    assert session._imap is None and session._smtp is None
    assert "not-shown" not in repr(session)


def test_the_factory_reads_the_environment(monkeypatch) -> None:
    monkeypatch.setenv("MAIL_IMAP_PORT", "2143")
    monkeypatch.setenv("MAIL_SMTP_HOST", "mail.test")
    factory = MailClientFactory()
    assert (factory.imap_host, factory.imap_port) == ("127.0.0.1", 2143)
    assert (factory.smtp_host, factory.smtp_port) == ("mail.test", 1588)

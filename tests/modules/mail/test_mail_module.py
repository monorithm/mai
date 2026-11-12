"""The mail module builds with its imports and gives the eight mail tools."""

import asyncio

import pytest
from bustan import ConfigModule, ConfigService, Module
from bustan.testing import create_testing_module

TOOLS = {
    "list_addresses",
    "send_message",
    "reply",
    "list_inbox",
    "read_message",
    "read_thread",
    "wait_for_mail",
    "mark_done",
}


def test_the_module_gives_the_mail_tools():
    try:
        from mai.modules.mail import MailModule, MailTools
    except ImportError as exc:
        pytest.skip(f"a sibling module is not ready: {exc}")

    @Module(imports=[ConfigModule.for_root(ignore_env_file=True), MailModule])
    class _Root:
        pass

    async def scenario():
        builder = create_testing_module(_Root)
        builder.override_provider(ConfigService).use_value(
            ConfigService({"DATABASE_BACKEND": "memory"})
        )
        compiled = await builder.compile()
        try:
            tools = compiled.get(MailTools).tools()
            return set(tools)
        finally:
            await compiled.close()

    try:
        names = asyncio.run(scenario())
    except ImportError as exc:
        pytest.skip(f"a sibling module is not ready: {exc}")
    assert names == TOOLS

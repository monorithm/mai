import pytest
from bustan import ConfigModule, Module
from bustan.testing import create_testing_module

from mai.modules.notices import NoticesModule, WatchService
from mai.modules.threads import ClaimService, ThreadsModule, ThreadTools
from mai.modules.threads.infra.in_memory_claim_store import InMemoryClaimStore

pytestmark = pytest.mark.anyio


@pytest.fixture
def anyio_backend():
    return "asyncio"


@Module(imports=[ConfigModule.for_root(), ThreadsModule, NoticesModule])
class Root:
    pass


async def test_module_wires_memory_adapter_and_tools(monkeypatch):
    monkeypatch.setenv("THREADS_ROUTER", "off")
    monkeypatch.setenv("THREADS_STALE_SIGNAL", "off")
    compiled = await create_testing_module(Root).compile()
    try:
        tools = compiled.get(ThreadTools)
        assert set(tools.tools()) == {"claim_thread", "release_thread"}
        assert isinstance(compiled.get(ClaimService)._store, InMemoryClaimStore)
        assert isinstance(compiled.get(WatchService), WatchService)
    finally:
        await compiled.close()

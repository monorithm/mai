"""Host tests of the board module. When the sessions or threads module is not importable in this tree,
a stand-in module with the exported names takes its place, thus the board code imports."""

import importlib
import sys
import types


def _stand_in(name, **names):
    try:
        module = importlib.import_module(name)
    except ImportError:
        module = None
    if module is None or not all(hasattr(module, key) for key in names):
        module = types.ModuleType(name)
        for key, value in names.items():
            setattr(module, key, value)
        sys.modules[name] = module


class _SessionService:
    async def resolve(self, handle):
        raise NotImplementedError


class _ClaimService:
    async def claim(self, state, ref=None, thread_ref=None):
        raise NotImplementedError


class _Module:
    pass


_stand_in(
    "mai.modules.sessions", SessionService=_SessionService, SessionsModule=_Module
)
_stand_in("mai.modules.threads", ClaimService=_ClaimService, ThreadsModule=_Module)

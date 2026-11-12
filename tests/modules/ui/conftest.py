"""Host tests of the ui module. When the board module is not importable in this tree, a
stand-in module with the exported name takes its place, thus the ui code imports."""

import importlib
import sys
import types

try:
    importlib.import_module("mai.modules.board")
except ImportError:
    for name in [n for n in sys.modules if n.startswith("mai.modules.board")]:
        del sys.modules[name]
    stand_in = types.ModuleType("mai.modules.board")

    class BoardTools:
        def tools(self):
            return {}

    setattr(stand_in, "BoardTools", BoardTools)  # noqa: B010
    sys.modules["mai.modules.board"] = stand_in

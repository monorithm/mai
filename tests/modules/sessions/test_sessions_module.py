"""The sessions module builds with its imports and gives its exports."""

from bustan import ConfigModule, Module
from bustan.testing import create_test_app

from mai.modules.sessions import SessionService, SessionsModule, SessionTools


@Module(imports=[ConfigModule.for_root(), SessionsModule])
class Root:
    pass


def test_module_builds():
    app = create_test_app(Root)
    tools = app.get(SessionTools)
    assert set(tools.tools()) == {"logout", "whoami"}
    assert isinstance(app.get(SessionService), SessionService)

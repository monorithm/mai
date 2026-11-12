"""The ui module: the owner pages under /ui."""

from .infra.postgres_ui_state import MIGRATIONS
from .ui_module import UiModule

__all__ = ["MIGRATIONS", "UiModule"]

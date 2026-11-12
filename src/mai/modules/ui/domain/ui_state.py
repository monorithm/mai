"""The state of the UI: on or off. The default is off."""

from __future__ import annotations

from abc import ABC, abstractmethod


class UiOffError(Exception):
    code = "UI_OFF"

    def __init__(self) -> None:
        super().__init__("the UI is off")


class UiStateStore(ABC):
    @abstractmethod
    async def enabled(self) -> bool: ...

    @abstractmethod
    async def set_enabled(self, enabled: bool) -> None: ...

import threading

from bustan import Injectable

from ..domain.session_state import SessionState


@Injectable()
class SessionCache:
    """The live sessions of this process by handle. One instance for the application."""

    def __init__(self) -> None:
        self._states: dict[str, SessionState] = {}
        self._lock = threading.Lock()

    def put(self, state: SessionState) -> None:
        with self._lock:
            self._states[state.handle] = state

    def get(self, handle: str) -> SessionState | None:
        with self._lock:
            return self._states.get(handle)

    def pop(self, handle: str) -> SessionState | None:
        with self._lock:
            return self._states.pop(handle, None)

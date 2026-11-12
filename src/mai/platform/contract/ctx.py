"""The call context that the MCP dispatcher gives to each tool."""


class Ctx:
    """session_state is the resolved session of the call, or None before a login. transport is "stdio" or "http"."""

    def __init__(self, session_state=None, transport="stdio"):
        if transport not in ("stdio", "http"):
            raise ValueError("transport must be stdio or http")
        self.session_state = session_state
        self.transport = transport

    @property
    def logged_in(self):
        return self.session_state is not None

"""Tool error codes of the mai contract. Each code has one constant message that never carries mail text."""

MESSAGES = {
    "INVALID_ARGUMENT": "An argument does not agree with the input schema.",
    "LOGIN_FAILED": "The login or the secret is not correct.",
    "CREDENTIAL_EXPIRED": "The credential expired.",
    "CREDENTIAL_REVOKED": "The owner revoked the credential.",
    "SESSION_UNKNOWN": "The session handle is unknown, expired, or ended.",
    "NOT_LOGGED_IN": "Call login first.",
    "FORBIDDEN_ROLE": "The roles of the session do not include this tool.",
    "RECIPIENT_UNKNOWN": "An address is not an account in mai.internal.",
    "UNKNOWN_MESSAGE": "No mailbox of the session has this ref.",
    "BODY_TOO_LARGE": "The body is more than 64 KB.",
    "REFUSED_HEADER": "The subject has a control character or a line break.",
    "RETRY_KEY_REUSED": "The session used this request_id with different content.",
    "THREAD_CLAIMED": "A different session holds this thread.",
    "MAIL_SERVER_DOWN": "The mail server does not reply.",
    "STORE_UNAVAILABLE": "The store does not reply.",
    "BUSY": "The server is busy.",
    "INTERNAL": "An internal error occurred.",
    "BRIDGE_ONLY": "Only the bridge mai mcp answers this tool. Use the MCP entry that mai connect writes.",
    "INVALID_TRANSITION": "The item cannot go to this state from its current state.",
    "WIP_LIMIT": "The lane has its maximum number of items in progress.",
    "SUPERVISOR_TAKEN": "The project already has a supervisor.",
    "EVIDENCE_INCOMPLETE": "The evidence is not complete.",
    "DEPENDENCY_OPEN": "A dependency of the item is not done.",
    "UNKNOWN_ITEM": "The item does not exist.",
    "UNKNOWN_PROJECT": "The project does not exist.",
}

RETRY_SAFE = {
    "THREAD_CLAIMED",
    "MAIL_SERVER_DOWN",
    "STORE_UNAVAILABLE",
    "BUSY",
    "INTERNAL",
}


class ToolError(Exception):
    """A tool failure that the client sees as isError with a code, a constant message, a path, and retry_safe."""

    def __init__(self, code, path=None, **extra):
        super().__init__(code)
        self.code = code if code in MESSAGES else "INTERNAL"
        self.path = path
        self.extra = extra

    @property
    def message(self):
        return MESSAGES[self.code]

    def structured(self):
        body = {
            "error": {
                "code": self.code,
                "message": self.message,
                "path": self.path,
                "retry_safe": self.code in RETRY_SAFE,
            }
        }
        body.update(self.extra)
        return body

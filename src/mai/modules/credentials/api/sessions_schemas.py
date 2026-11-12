from pydantic import BaseModel, Field


class NewSessionRequest(BaseModel):
    agent: str = Field(min_length=1, max_length=64)
    label: str = Field(min_length=1, max_length=32)
    expires: str | int = 7


class SessionResponse(BaseModel):
    login: str
    address: str
    kind: str | None = None
    agent: str | None = None
    label: str | None = None
    status: str | None = None
    expires_at: str | None = None
    created_at: str | None = None
    last_login_at: str | None = None


class IssuedSessionResponse(BaseModel):
    login: str
    address: str
    credential: str
    expires_at: str | None = None

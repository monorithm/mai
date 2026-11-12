from typing import Annotated

from bustan import (
    ApiBearerAuth,
    ApiOperation,
    ApiResponse,
    ApiTags,
    Controller,
    Get,
    HttpResponse,
    Post,
)
from bustan.errors import (
    BadRequestException,
    ConflictException,
    NotFoundException,
    UnauthorizedException,
)

from ..application.credential_service import CredentialService
from ..domain.errors import AccountStateError, UnknownAccountError
from .owner_token import OwnerToken
from .sessions_schemas import IssuedSessionResponse, NewSessionRequest, SessionResponse


# The owner API of the session logins. Each route needs the owner credential
# "mai1:owner:<secret>" as the bearer token.
@Controller("/api/sessions")
@ApiTags("sessions")
class SessionsController:
    def __init__(self, credentials: CredentialService) -> None:
        self._credentials = credentials

    async def _owner(self, token: str) -> None:
        if not token or await self._credentials.authenticate_owner(token) is None:
            raise UnauthorizedException("the owner credential is required")

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="List the session logins")
    @ApiResponse(status=200, description="The session logins", schema=SessionResponse)
    @ApiResponse(status=401, description="No owner credential")
    async def list_sessions(
        self, token: Annotated[str, OwnerToken()]
    ) -> dict[str, object]:
        await self._owner(token)
        return {"sessions": await self._credentials.session_list()}

    @Post("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Make a session login with a new credential")
    @ApiResponse(
        status=201, description="The new credential", schema=IssuedSessionResponse
    )
    @ApiResponse(status=400, description="The agent or the label is not valid")
    @ApiResponse(status=401, description="No owner credential")
    @ApiResponse(status=409, description="The login exists")
    async def new_session(
        self, payload: NewSessionRequest, token: Annotated[str, OwnerToken()]
    ) -> HttpResponse:
        await self._owner(token)
        try:
            issued = await self._credentials.session_new(
                payload.agent, payload.label, expires_days=payload.expires
            )
        except AccountStateError as exc:
            raise ConflictException(str(exc)) from None
        except ValueError as exc:
            raise BadRequestException(str(exc)) from None
        return HttpResponse.json(issued, status_code=201)

    @Post("/{login}/rotate")
    @ApiBearerAuth()
    @ApiOperation(summary="Give a session login a new secret")
    @ApiResponse(
        status=200, description="The new credential", schema=IssuedSessionResponse
    )
    @ApiResponse(status=401, description="No owner credential")
    @ApiResponse(status=404, description="No such session login")
    @ApiResponse(status=409, description="The session is revoked or expired")
    async def rotate_session(
        self, login: str, token: Annotated[str, OwnerToken()]
    ) -> dict[str, object]:
        await self._owner(token)
        try:
            return await self._credentials.session_rotate(login)
        except UnknownAccountError as exc:
            raise NotFoundException(str(exc)) from None
        except AccountStateError as exc:
            raise ConflictException(str(exc)) from None

    @Post("/{login}/revoke")
    @ApiBearerAuth()
    @ApiOperation(summary="Revoke a session login")
    @ApiResponse(status=200, description="The login is revoked")
    @ApiResponse(status=401, description="No owner credential")
    @ApiResponse(status=404, description="No such session login")
    async def revoke_session(
        self, login: str, token: Annotated[str, OwnerToken()]
    ) -> dict[str, object]:
        await self._owner(token)
        try:
            return await self._credentials.session_revoke(login)
        except UnknownAccountError as exc:
            raise NotFoundException(str(exc)) from None

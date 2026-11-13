from bustan import (
    ApiBearerAuth,
    ApiOperation,
    ApiResponse,
    ApiTags,
    Controller,
    Delete,
    Get,
    HttpRequest,
    HttpResponse,
    Post,
)

from mai.modules.credentials import CredentialService

from ..application.lease_service import LeaseError
from ..application.owner_sessions import OwnerSessions
from .leases_controller import api_error, bearer, json_body, lease_error


# The owner API of the sessions. Each route needs the owner key
# "mai1:owner:<secret>" as the bearer token. No answer holds a session secret:
# the server keeps each secret, and a bridge gets a session only through a lease.
@Controller("/api/sessions")
@ApiTags("sessions")
class SessionsController:
    def __init__(self, sessions: OwnerSessions, credentials: CredentialService) -> None:
        self._sessions = sessions
        self._credentials = credentials

    async def _is_owner(self, request: HttpRequest) -> bool:
        token = bearer(request)
        return (
            bool(token)
            and await self._credentials.authenticate_owner(token) is not None
        )

    @Post("/")
    @ApiBearerAuth()
    @ApiOperation(
        summary="Make the named session AGENT.LABEL, or the pool sessions AGENT.s1 to AGENT.sCOUNT"
    )
    @ApiResponse(status=201, description="The session, or the sessions that were made")
    @ApiResponse(status=400, description="The request or the label is not valid")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="No such agent")
    @ApiResponse(status=409, description="The session exists")
    async def new_session(self, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        body = await json_body(request)
        agent, label, count = body.get("agent"), body.get("label"), body.get("count")
        if not isinstance(agent, str):
            return api_error(400, "BAD_REQUEST", "agent must be a string")
        if (label is None) == (count is None):
            return api_error(400, "BAD_REQUEST", "give label or count, not both")
        try:
            if label is not None:
                made = await self._sessions.new(agent, label)
                return HttpResponse.json(made, status_code=201)
            pool = await self._sessions.new_pool(agent, count)
        except LeaseError as exc:
            return lease_error(exc)
        return HttpResponse.json({"sessions": pool}, status_code=201)

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="List the sessions, their status, and their leases")
    @ApiResponse(status=200, description="The sessions")
    @ApiResponse(status=401, description="No owner key")
    async def list_sessions(self, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        return HttpResponse.json({"sessions": await self._sessions.with_leases()})

    @Post("/{login}/rotate")
    @ApiBearerAuth()
    @ApiOperation(summary="Give a session a new internal secret, and end its lease")
    @ApiResponse(status=200, description="The session is active")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="No such session")
    @ApiResponse(status=409, description="The session is revoked")
    async def rotate_session(self, login: str, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        try:
            return HttpResponse.json(await self._sessions.rotate(login))
        except LeaseError as exc:
            return lease_error(exc)

    @Post("/{login}/revoke")
    @ApiBearerAuth()
    @ApiOperation(summary="Close a session for good, and end its lease")
    @ApiResponse(status=200, description="The session is revoked")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="No such session")
    async def revoke_session(self, login: str, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        try:
            return HttpResponse.json(await self._sessions.revoke(login))
        except LeaseError as exc:
            return lease_error(exc)

    @Delete("/{login}/lease")
    @ApiBearerAuth()
    @ApiOperation(summary="End the lease of a session")
    @ApiResponse(status=204, description="The lease ended")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="The session has no lease")
    async def end_lease(self, login: str, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        if not await self._sessions.end_lease(login):
            return api_error(404, "NOT_FOUND", f"no lease on the session: {login}")
        return HttpResponse(status_code=204, body=b"")

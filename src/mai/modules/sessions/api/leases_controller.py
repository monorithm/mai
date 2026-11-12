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

from ..application.lease_service import LeaseError, LeaseService

_STATUS = {"BAD_KEY": 401, "LEASE_UNKNOWN": 401, "NO_FREE_SESSION": 409}


def bearer(request: HttpRequest) -> str:
    """The bearer token of the request, or "" when there is none."""
    header = request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    return token.strip() if scheme.lower() == "bearer" else ""


def api_error(status: int, code: str, message: str) -> HttpResponse:
    return HttpResponse.json(
        {"error": {"code": code, "message": message}}, status_code=status
    )


def lease_error(exc: LeaseError) -> HttpResponse:
    return api_error(_STATUS.get(exc.code, 400), exc.code, str(exc))


# The lease API. A bridge sends the agent key to get a lease, then the lease
# token for each heartbeat and for the end of the lease.
@Controller("/api/leases")
@ApiTags("leases")
class LeasesController:
    def __init__(self, leases: LeaseService, credentials: CredentialService) -> None:
        self._leases = leases
        self._credentials = credentials

    @Post("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Get a lease on a free session of the agent")
    @ApiResponse(status=201, description="The lease")
    @ApiResponse(status=401, description="The agent key is not valid")
    @ApiResponse(status=409, description="The agent has no free session")
    async def acquire(self, request: HttpRequest) -> HttpResponse:
        try:
            body = await request.json()
        except ValueError:
            body = None
        body = body if isinstance(body, dict) else {}
        app, folder, pid = body.get("app"), body.get("folder"), body.get("pid")
        if (
            not isinstance(app, str)
            or not isinstance(folder, str)
            or not isinstance(pid, int)
        ):
            return api_error(
                400, "BAD_REQUEST", "app and folder must be strings, pid an integer"
            )
        try:
            lease = await self._leases.acquire(bearer(request), app, folder, pid)
        except LeaseError as exc:
            return lease_error(exc)
        return HttpResponse.json(lease, status_code=201)

    @Post("/current/heartbeat")
    @ApiBearerAuth()
    @ApiOperation(summary="Keep the lease live and get the count of new mail")
    @ApiResponse(status=200, description="The session and the count of new mail")
    @ApiResponse(status=401, description="The lease ended or expired")
    async def heartbeat(self, request: HttpRequest) -> HttpResponse:
        try:
            return HttpResponse.json(await self._leases.heartbeat(bearer(request)))
        except LeaseError as exc:
            return lease_error(exc)

    @Delete("/current")
    @ApiBearerAuth()
    @ApiOperation(summary="End the lease")
    @ApiResponse(status=204, description="The lease ended")
    @ApiResponse(status=401, description="The lease ended or expired")
    async def end(self, request: HttpRequest) -> HttpResponse:
        try:
            await self._leases.end(bearer(request))
        except LeaseError as exc:
            return lease_error(exc)
        return HttpResponse(status_code=204, body=b"")

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="List the leases")
    @ApiResponse(status=200, description="The leases")
    @ApiResponse(status=401, description="No owner key")
    async def list_leases(self, request: HttpRequest) -> HttpResponse:
        if await self._credentials.authenticate_owner(bearer(request)) is None:
            return api_error(401, "BAD_KEY", "the owner key is required")
        return HttpResponse.json({"leases": await self._leases.leases()})


# The owner view of the sessions and their leases.
@Controller("/api/sessions")
@ApiTags("sessions")
class SessionLeasesController:
    def __init__(self, leases: LeaseService, credentials: CredentialService) -> None:
        self._leases = leases
        self._credentials = credentials

    async def _is_owner(self, request: HttpRequest) -> bool:
        token = bearer(request)
        return (
            bool(token)
            and await self._credentials.authenticate_owner(token) is not None
        )

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="List the sessions and if each has a lease")
    @ApiResponse(status=200, description="The sessions")
    @ApiResponse(status=401, description="No owner key")
    async def list_sessions(self, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        return HttpResponse.json({"sessions": await self._leases.sessions()})

    @Delete("/{login}/lease")
    @ApiBearerAuth()
    @ApiOperation(summary="End the lease of a session")
    @ApiResponse(status=204, description="The lease ended")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="The session has no lease")
    async def end_lease(self, login: str, request: HttpRequest) -> HttpResponse:
        if not await self._is_owner(request):
            return api_error(401, "BAD_KEY", "the owner key is required")
        if not await self._leases.end_session(login):
            return api_error(404, "NOT_FOUND", f"no lease on the session: {login}")
        return HttpResponse(status_code=204, body=b"")

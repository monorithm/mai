import sys

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

_STATUS = {
    "BAD_REQUEST": 400,
    "BAD_LABEL": 400,
    "BAD_KEY": 401,
    "LEASE_UNKNOWN": 401,
    "AGENT_UNKNOWN": 404,
    "SESSION_UNKNOWN": 404,
    "NO_FREE_SESSION": 409,
    "SESSION_EXISTS": 409,
    "SESSION_REVOKED": 409,
    "SESSION_BUSY": 409,
    "INTERNAL": 500,
    "MAIL_SERVER_DOWN": 503,
    "STORE_UNAVAILABLE": 503,
}


def bearer(request: HttpRequest) -> str:
    """The bearer token of the request, or "" when there is none."""
    header = request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    return token.strip() if scheme.lower() == "bearer" else ""


def api_error(
    status: int, code: str, message: str, retry_safe: bool | None = None
) -> HttpResponse:
    error: dict = {"code": code, "message": message}
    if retry_safe is not None:
        error["retry_safe"] = retry_safe
    return HttpResponse.json({"error": error}, status_code=status)


def lease_error(exc: LeaseError) -> HttpResponse:
    status = exc.status or _STATUS.get(exc.code, 400)
    return api_error(status, exc.code, str(exc), exc.retry_safe)


def internal_error(exc: Exception) -> HttpResponse:
    """The coded answer of a defect. It shows no internal text."""
    sys.stderr.write(f"mai: internal error of type {type(exc).__name__}\n")
    return api_error(500, "INTERNAL", "an internal error occurred", True)


async def json_body(request: HttpRequest) -> dict:
    """The JSON object of the request body, or {} when the body is not an object."""
    try:
        body = await request.json()
    except ValueError:
        body = None
    return body if isinstance(body, dict) else {}


# The lease API. A bridge sends the agent key to get a lease, then the lease
# token for each heartbeat and for the end of the lease. The optional field
# "session" of a lease request names a session of the agent, for example
# "lane-a" or "cursor.lane-a". With no "session", the lease is on a pool
# session. When no pool session is free, the lease has the session null until
# its first tool call. Each error answer has a code.
@Controller("/api/leases")
@ApiTags("leases")
class LeasesController:
    def __init__(self, leases: LeaseService, credentials: CredentialService) -> None:
        self._leases = leases
        self._credentials = credentials

    @Post("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Get a lease on a named session or a pool session")
    @ApiResponse(
        status=201,
        description="The lease. The session is null when no pool session is free.",
    )
    @ApiResponse(status=400, description="The request or the label is not valid")
    @ApiResponse(status=401, description="The agent key is not valid")
    @ApiResponse(status=404, description="The agent has no such session")
    @ApiResponse(
        status=409,
        description="The session is revoked or busy, or the agent has no free session",
    )
    @ApiResponse(status=502, description="The mail login failed")
    @ApiResponse(status=503, description="The mail server or the store does not reply")
    async def acquire(self, request: HttpRequest) -> HttpResponse:
        body = await json_body(request)
        app, folder, pid = body.get("app"), body.get("folder"), body.get("pid")
        session = body.get("session")
        if (
            not isinstance(app, str)
            or not isinstance(folder, str)
            or not isinstance(pid, int)
            or isinstance(pid, bool)
        ):
            return api_error(
                400, "BAD_REQUEST", "app and folder must be strings, pid an integer"
            )
        if session is not None and not isinstance(session, str):
            return api_error(400, "BAD_REQUEST", "session must be a string")
        try:
            lease = await self._leases.acquire(
                bearer(request), app, folder, pid, session
            )
        except LeaseError as exc:
            return lease_error(exc)
        except Exception as exc:  # noqa: BLE001 - each error answer has a code
            return internal_error(exc)
        return HttpResponse.json(lease, status_code=201)

    @Post("/current/heartbeat")
    @ApiBearerAuth()
    @ApiOperation(summary="Keep the lease live and get the count of new mail")
    @ApiResponse(
        status=200,
        description="The session and the count of new mail. The count is null when it failed.",
    )
    @ApiResponse(status=401, description="The lease ended or expired")
    @ApiResponse(status=503, description="The store does not reply")
    async def heartbeat(self, request: HttpRequest) -> HttpResponse:
        try:
            return HttpResponse.json(await self._leases.heartbeat(bearer(request)))
        except LeaseError as exc:
            return lease_error(exc)
        except Exception as exc:  # noqa: BLE001 - each error answer has a code
            return internal_error(exc)

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
        except Exception as exc:  # noqa: BLE001 - each error answer has a code
            return internal_error(exc)
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


# The owner list of the agents, with the count of the sessions and leases of each.
@Controller("/api/agents")
@ApiTags("agents")
class AgentLeasesController:
    def __init__(self, leases: LeaseService, credentials: CredentialService) -> None:
        self._leases = leases
        self._credentials = credentials

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="List the agents")
    @ApiResponse(status=200, description="The agents")
    @ApiResponse(status=401, description="No owner key")
    async def list_agents(self, request: HttpRequest) -> HttpResponse:
        token = bearer(request)
        if not token or await self._credentials.authenticate_owner(token) is None:
            return api_error(401, "BAD_KEY", "the owner key is required")
        agents = []
        for row in await self._credentials.agent_list():
            if row["status"] != "active":
                continue
            agents.append(
                {
                    "name": row["agent"],
                    "sessions": row["sessions"],
                    "leases": await self._leases.lease_count(row["agent"]),
                }
            )
        return HttpResponse.json({"agents": agents})

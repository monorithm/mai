from bustan import Controller, Get, HttpRequest, HttpResponse

from mai import __version__
from mai.modules.credentials import CredentialService
from mai.platform.health import HealthChecks

from ..application.json_rpc import SERVER_INFO


def _bearer(request: HttpRequest) -> str:
    header = request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    return token.strip() if scheme.lower() == "bearer" else ""


# GET /api/version needs no key: the CLI reads it before it has a key.
# GET /api/health needs the owner key.
@Controller("/api")
class ServerController:
    def __init__(self, credentials: CredentialService, checks: HealthChecks) -> None:
        self._credentials = credentials
        self._checks = checks

    @Get("/version")
    async def version(self) -> HttpResponse:
        return HttpResponse.json(
            {"version": __version__, "contract": SERVER_INFO["version"]}
        )

    @Get("/health")
    async def health(self, request: HttpRequest) -> HttpResponse:
        token = _bearer(request)
        if not token or await self._credentials.authenticate_owner(token) is None:
            return HttpResponse.json(
                {"error": {"code": "BAD_KEY", "message": "the owner key is required"}},
                status_code=401,
            )
        ready = await self._checks.ready()
        children = {"postgres": "ok" if ready else "down"}
        return HttpResponse.json({"healthy": ready, "children": children})

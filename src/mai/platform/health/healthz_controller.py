from bustan import Controller, Get, HttpResponse, Injectable

from .health_checks_module import HealthChecks


@Controller("/healthz")
@Injectable()
class HealthzController:
    """GET /healthz: 200 when the process serves and the database answers.

    The container health check calls this path. The bustan probes
    /health/live and /health/ready are also served.
    """

    def __init__(self, checks: HealthChecks) -> None:
        self._checks = checks

    @Get("/")
    async def healthz(self) -> HttpResponse:
        ok = await self._checks.ready()
        return HttpResponse.json({"ok": ok}, status_code=200 if ok else 503)

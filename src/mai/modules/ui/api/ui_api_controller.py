"""The owner API of the UI: the state on or off, and the sign-in tickets.

Each route needs the owner key "mai1:owner:<secret>" as the bearer token. The
check uses the owner authentication of the owner API (OwnerToken and
CredentialService.authenticate_owner).
"""

from __future__ import annotations

import json
from typing import Annotated

from bustan import (
    ApiBearerAuth,
    ApiOperation,
    ApiTags,
    Controller,
    Get,
    HttpRequest,
    HttpResponse,
    Post,
    Put,
)

from mai.modules.credentials import CredentialService, OwnerToken

from ..application.ui_access_service import UiAccessService
from ..domain.ui_state import UiOffError

MAX_BODY = 4096


def _error(status: int, code: str, message: str) -> HttpResponse:
    return HttpResponse.json(
        {"error": {"code": code, "message": message}}, status_code=status
    )


@Controller("/api/ui")
@ApiTags("ui")
class UiApiController:
    def __init__(self, credentials: CredentialService, access: UiAccessService) -> None:
        self._credentials = credentials
        self._access = access

    async def _is_owner(self, token: str) -> bool:
        return (
            bool(token)
            and await self._credentials.authenticate_owner(token) is not None
        )

    @Get("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Give the state of the UI")
    async def get_state(self, token: Annotated[str, OwnerToken()]) -> HttpResponse:
        if not await self._is_owner(token):
            return _error(401, "BAD_KEY", "the owner key is required")
        return HttpResponse.json({"enabled": await self._access.enabled()})

    @Put("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Turn the UI on or off")
    async def put_state(
        self, request: HttpRequest, token: Annotated[str, OwnerToken()]
    ) -> HttpResponse:
        if not await self._is_owner(token):
            return _error(401, "BAD_KEY", "the owner key is required")
        body = await request.body()
        try:
            data = json.loads(body) if len(body) <= MAX_BODY else None
        except ValueError:
            data = None
        if not isinstance(data, dict) or not isinstance(data.get("enabled"), bool):
            return _error(
                400, "BAD_REQUEST", 'the body must be {"enabled": true|false}'
            )
        enabled = await self._access.set_enabled(data["enabled"])
        return HttpResponse.json({"enabled": enabled})

    @Post("/tickets")
    @ApiBearerAuth()
    @ApiOperation(summary="Make a sign-in ticket, valid one time for 60 s")
    async def new_ticket(self, token: Annotated[str, OwnerToken()]) -> HttpResponse:
        if not await self._is_owner(token):
            return _error(401, "BAD_KEY", "the owner key is required")
        try:
            issued = await self._access.issue_ticket()
        except UiOffError as exc:
            return _error(409, exc.code, str(exc))
        return HttpResponse.json(
            {"ticket": issued.ticket, "expires_seconds": issued.expires_seconds},
            status_code=201,
        )

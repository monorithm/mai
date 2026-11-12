from typing import Annotated

from bustan import (
    ApiBearerAuth,
    ApiOperation,
    ApiResponse,
    ApiTags,
    Controller,
    Delete,
    HttpRequest,
    HttpResponse,
    Post,
)

from ..application.agent_keys import AgentKeys
from ..application.credential_service import CredentialService
from ..domain.errors import AccountStateError, UnknownAccountError
from .api_errors import api_error
from .owner_token import OwnerToken


# The owner API of the agents. The sessions module lists the agents with their leases. Each route needs the owner key
# "mai1:owner:<secret>" as the bearer token. The answer that makes a key is the
# only answer that holds it.
@Controller("/api/agents")
@ApiTags("agents")
class AgentsController:
    def __init__(self, credentials: CredentialService, keys: AgentKeys) -> None:
        self._credentials = credentials
        self._keys = keys

    async def _is_owner(self, token: str) -> bool:
        return (
            bool(token)
            and await self._credentials.authenticate_owner(token) is not None
        )

    @Post("/")
    @ApiBearerAuth()
    @ApiOperation(summary="Make an agent and its key")
    @ApiResponse(status=201, description="The agent and its key")
    @ApiResponse(status=400, description="The name is not valid")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=409, description="The agent exists")
    async def add_agent(
        self, request: HttpRequest, token: Annotated[str, OwnerToken()]
    ) -> HttpResponse:
        if not await self._is_owner(token):
            return api_error(401, "BAD_KEY", "the owner key is required")
        try:
            body = await request.json()
        except ValueError:
            body = None
        name = body.get("name") if isinstance(body, dict) else None
        if not isinstance(name, str):
            return api_error(400, "BAD_REQUEST", "name must be a string")
        try:
            key = await self._keys.add(name)
        except AccountStateError as exc:
            return api_error(409, "AGENT_EXISTS", str(exc))
        except ValueError as exc:
            return api_error(400, "BAD_REQUEST", str(exc))
        return HttpResponse.json({"name": name, "agent_key": key}, status_code=201)

    @Post("/{name}/key")
    @ApiBearerAuth()
    @ApiOperation(summary="Give an agent a new key")
    @ApiResponse(status=200, description="The new key")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="No such agent")
    async def rotate_key(
        self, name: str, token: Annotated[str, OwnerToken()]
    ) -> HttpResponse:
        if not await self._is_owner(token):
            return api_error(401, "BAD_KEY", "the owner key is required")
        try:
            key = await self._keys.rotate(name)
        except UnknownAccountError as exc:
            return api_error(404, "NOT_FOUND", str(exc))
        return HttpResponse.json({"agent_key": key})

    @Delete("/{name}")
    @ApiBearerAuth()
    @ApiOperation(summary="Remove an agent")
    @ApiResponse(status=204, description="The agent is removed")
    @ApiResponse(status=401, description="No owner key")
    @ApiResponse(status=404, description="No such agent")
    async def remove_agent(
        self, name: str, token: Annotated[str, OwnerToken()]
    ) -> HttpResponse:
        if not await self._is_owner(token):
            return api_error(401, "BAD_KEY", "the owner key is required")
        try:
            await self._keys.remove(name)
        except UnknownAccountError as exc:
            return api_error(404, "NOT_FOUND", str(exc))
        return HttpResponse(status_code=204, body=b"")

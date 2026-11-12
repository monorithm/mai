from typing import cast

from bustan import ExecutionContext, create_param_decorator


def _owner_token(data: object | None, context: object) -> str:
    request = cast(ExecutionContext, context).request
    header = "" if request is None else request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    return token.strip() if scheme.lower() == "bearer" else ""


OwnerToken = create_param_decorator(_owner_token, name="OwnerToken")
"""Give the handler the bearer token of the request, or "" when there is none."""

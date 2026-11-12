"""The error body of the HTTP API: {"error": {"code": CODE, "message": TEXT}}."""

from bustan import HttpResponse


def api_error(status: int, code: str, message: str) -> HttpResponse:
    """An error answer with the status STATUS and the code CODE."""
    return HttpResponse.json(
        {"error": {"code": code, "message": message}}, status_code=status
    )

"""The hold protocol between the container controller and a command that stops daemons.

The controller starts each daemon again when it stops. A command that must
stop daemons for a time, for example restore, takes the exclusive lock and
writes the names and its PID to the hold file. The controller then stops the
held daemons and does not start them. The command waits until the state file
shows that they stopped. When the command removes the hold, the controller
starts them again, and the command waits until the state file shows a new
process. If the command stops before it removes the hold, the hold is not
valid any more: the controller removes the file and starts the daemons.
"""

from .protocol import (
    CONTROLLER_FILE,
    HOLD_FILE,
    LOCK_FILE,
    STATE_FILE,
    HoldBusy,
    exclusive,
    held,
    hold,
    mark_controller,
    read_hold,
    release,
    stale,
    states,
    supervised,
    wait_ready,
    wait_restarted,
    wait_stopped,
)

__all__ = [
    "CONTROLLER_FILE",
    "HOLD_FILE",
    "LOCK_FILE",
    "STATE_FILE",
    "HoldBusy",
    "exclusive",
    "held",
    "hold",
    "mark_controller",
    "read_hold",
    "release",
    "stale",
    "states",
    "supervised",
    "wait_ready",
    "wait_restarted",
    "wait_stopped",
]

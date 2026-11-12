"""The tool read_thread: the messages of one thread, oldest first, and the session that holds its claim."""

from bustan import Injectable

from mai.modules.threads import ClaimService

from ..domain import mail_rules as r
from ..domain.errors import MailError
from .mail_reader import MailReader, boxes_of, by_date


@Injectable()
class ReadThreadHandler:
    def __init__(self, reader: MailReader, claims: ClaimService) -> None:
        self._reader = reader
        self._claims = claims

    async def execute(self, ctx, args: dict) -> dict:
        state = await self._reader.state_for(ctx, args)
        limit = r.limit_arg(args, 100, 100)
        thread_ref = args.get("thread_ref")
        ref = args.get("ref")
        if bool(thread_ref) == bool(ref):
            raise MailError("INVALID_ARGUMENT", path="thread_ref")
        r.check_thread_ref(thread_ref)
        async with self._reader.lock(state):
            entries = await self._reader.scan(
                state, boxes_of(state, done=True, sent=True)
            )
            if ref:
                r.check_ref(ref)
                match = [e for e in entries if e["ref"] == ref]
                if not match:
                    raise MailError("UNKNOWN_MESSAGE", path="ref")
                thread_ref = r.thread_ref_of(match[0]["root"])
            members: dict[str, dict] = {}
            for e in entries:
                if r.thread_ref_of(e["root"]) == thread_ref and e["ref"] not in members:
                    members[e["ref"]] = e
            if not members:
                raise MailError("UNKNOWN_MESSAGE", path="thread_ref")
            ordered = by_date(members.values(), newest_first=False)
            truncated = len(ordered) > limit
            messages = [await self._reader.full(state, e) for e in ordered[:limit]]
        owner = None
        try:
            owner = await self._claims.holder(state.agent, ordered[0]["root"])
        except Exception:  # noqa: BLE001
            # The owner is a hint; a store that is down gives no owner.
            owner = None
        return {
            "notice": r.NOTICE,
            "thread_ref": thread_ref,
            "owner": owner,
            "messages": messages,
            "truncated": truncated,
        }

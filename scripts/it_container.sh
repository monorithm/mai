#!/bin/sh
# Start a mai container from an image, add the test agents, make the test credentials,
# and run the container tests.
# Usage: scripts/it_container.sh [IMAGE]   (default $MAI_IT_IMAGE, else mai:dev)
# MAI_IT_IMAGE sets the image of the container and of the milter test.
# MAI_IT_NAME, MAI_IT_VOLUME, and MAI_IT_PORT set the names of the container
# and the volume, and the host port.
# It removes the container and its volume at the end. It never pulls an image.
set -eu
image=${1:-${MAI_IT_IMAGE:-mai:dev}}
name=${MAI_IT_NAME:-mai-it}
volume=${MAI_IT_VOLUME:-mai-it-data}
port=${MAI_IT_PORT:-18765}
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$volume-spool" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker volume create "$volume" >/dev/null
docker run -d --pull=never --name "$name" --read-only \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --cap-add SETGID --cap-add SETUID --cap-add KILL \
  --tmpfs /run --tmpfs /tmp --tmpfs /etc/postfix --tmpfs /etc/dovecot \
  -e MAI_HOSTS="127.0.0.1:$port,localhost:$port" -p "127.0.0.1:$port:8080" \
  -e THREADS_AGENT_REFRESH=2 \
  -v "$volume:/data" -v "$volume-spool:/var/lib/mai/spool" "$image" >/dev/null
i=0
until docker exec "$name" mai health >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then docker logs "$name"; exit 1; fi
  sleep 1
done
# No agent exists at the start. The router starts the worker of a new agent at
# its next read of the agent list, after THREADS_AGENT_REFRESH seconds.
docker exec "$name" mai agent add cursor codex copilot architect >/dev/null
sleep 3
docker exec "$name" mai project seed itproj --supervisor architect --worker cursor >/dev/null
# The server CLI shows no session secret. The container tests read the test
# credentials from files in the container, thus a short program of the server
# writes them through the credential service. No secret leaves the container.
docker exec -i "$name" python - <<'EOF'
import asyncio

from bustan import create_app_context

from mai.app_module import AppModule
from mai.modules.credentials import CredentialService

SESSIONS = (
    ("cursor", "lane-a", "/data/lanea.cred"),
    ("codex", "s", "/data/sender.cred"),
    ("copilot", "w", "/data/w.cred"),
    ("architect", "it-sup", "/data/sup.cred"),
    ("cursor", "it-wrk", "/data/wrk.cred"),
)


async def main():
    context = create_app_context(AppModule)
    await context.init()
    try:
        credentials = context.get(CredentialService)
        for agent, label, path in SESSIONS:
            await credentials.session_new(agent, label, file=path)
    finally:
        await context.close()


asyncio.run(main())
EOF
# The board test uses leases: set a test owner key, then get an agent key through
# the owner API. The test gets its leases itself. The keys stay in variables only.
okey="mai1:owner:$(LC_ALL=C tr -dc 'a-z2-7' </dev/urandom | head -c 52)"
printf '%s\n' "$okey" | docker exec -i "$name" mai owner set-key --replace >/dev/null
agent_key() {
  curl -fsS -X POST -H "Authorization: Bearer $okey" \
    "http://127.0.0.1:$port/api/agents/$1/key" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["agent_key"])'
}
MAI_IT=1 MAI_IT_IMAGE=$image MAI_IT_CONTAINER=$name MAI_IT_URL="http://127.0.0.1:$port/mcp" \
MAI_IT_PROJECT=itproj MAI_IT_LANE=cursor \
MAI_IT_SUPERVISOR_KEY=$(agent_key architect) \
MAI_IT_WORKER_KEY=$(agent_key cursor) \
  uv run --frozen pytest -q -p no:cacheprovider \
    tests/modules/threads/test_router_container_it.py \
    tests/modules/ops/test_ops_container_it.py \
    tests/platform/milter/test_milter_postfix.py \
    tests/modules/board/test_board_flow_it.py \
    tests/modules/mail/test_send_container_it.py

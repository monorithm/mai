#!/bin/sh
# Start a mai container from an image, add the test agents, make the test credentials,
# and run the container tests.
# Usage: scripts/it_container.sh [IMAGE]   (default mai:dev)
# It removes the container and its volume at the end. It never pulls an image.
set -eu
image=${1:-mai:dev}
name=mai-it
volume=mai-it-data
port=18765
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
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
  -v "$volume:/data" "$image" >/dev/null
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
docker exec "$name" sh -c '
  mai session new cursor --label lane-a --file /data/lanea.cred >/dev/null
  mai session new codex --label s --file /data/sender.cred >/dev/null
  mai session new copilot --label w --file /data/w.cred >/dev/null
  mai project seed itproj --supervisor architect --worker cursor >/dev/null
  mai session new architect --label it-sup --file /data/sup.cred >/dev/null
  mai session new cursor --label it-wrk --file /data/wrk.cred >/dev/null'
MAI_IT=1 MAI_IT_CONTAINER=$name MAI_IT_URL="http://127.0.0.1:$port/mcp" \
MAI_IT_PROJECT=itproj MAI_IT_LANE=cursor \
MAI_IT_SUPERVISOR_CRED=$(docker exec "$name" cat /data/sup.cred) \
MAI_IT_WORKER_CRED=$(docker exec "$name" cat /data/wrk.cred) \
  uv run --frozen pytest -q -p no:cacheprovider \
    tests/modules/threads/test_router_container_it.py \
    tests/modules/ops/test_ops_container_it.py \
    tests/platform/milter/test_milter_postfix.py \
    tests/modules/board/test_board_flow_it.py

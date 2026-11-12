#!/bin/sh
# Start a mai container, set the owner key, add an agent, and test the lease
# API and MCP with a lease.
# Usage: scripts/it_leases.sh [IMAGE]   (default mai:wf-leases)
# It removes the container and its volumes at the end. It never pulls an image.
# It never prints a key.
set -eu
image=${1:-mai:wf-leases}
name=mai-wf-leases-it
volume=mai-wf-leases-data
port=18775
base="http://127.0.0.1:$port"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$volume-spool" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
fail() {
  echo "FAIL: $1" >&2
  exit 1
}
json() {
  python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1"
}
docker volume create "$volume" >/dev/null
docker run -d --pull=never --name "$name" --read-only \
  --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
  --cap-add SETGID --cap-add SETUID --cap-add KILL \
  --tmpfs /run --tmpfs /tmp --tmpfs /etc/postfix --tmpfs /etc/dovecot \
  --tmpfs /var/lib/postgresql \
  -e MAI_HOSTS="127.0.0.1:$port" -p "127.0.0.1:$port:8080" \
  -v "$volume:/data" -v "$volume-spool:/var/lib/mai/spool" "$image" >/dev/null
i=0
until docker exec "$name" mai health >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 180 ]; then docker logs "$name" 2>&1 | tail -40; exit 1; fi
  sleep 1
done

owner=$(python3 -c 'import base64,secrets; print("mai1:owner:" + base64.b32encode(secrets.token_bytes(32)).decode().rstrip("=").lower())')
printf '%s\n' "$owner" | docker exec -i "$name" mai owner set-key >/dev/null
echo "ok: owner set-key"
set +e
printf '%s\n' "$owner" | docker exec -i "$name" mai owner set-key >/dev/null 2>&1
code=$?
set -e
[ "$code" = 3 ] || fail "a second set-key gave exit $code, not 3"
echo "ok: a second set-key gives exit 3"
if docker exec "$name" test -e /data/secrets/owner.cred; then
  fail "/data/secrets/owner.cred exists"
fi
echo "ok: no /data/secrets/owner.cred"

version=$(curl -fsS "$base/api/version")
[ "$(printf '%s' "$version" | json 'd["contract"]')" = 3.0.0 ] || fail "contract version"
echo "ok: GET /api/version"

agent=$(curl -fsS -X POST "$base/api/agents" -H "authorization: Bearer $owner" \
  -H 'content-type: application/json' -d '{"name":"cursor"}' | json 'd["agent_key"]')
case "$agent" in mai1:cursor:*) ;; *) fail "no agent key" ;; esac
echo "ok: POST /api/agents"

lease_json=$(curl -fsS -X POST "$base/api/leases" -H "authorization: Bearer $agent" \
  -H 'content-type: application/json' -d '{"app":"it","folder":"/it","pid":1}')
lease=$(printf '%s' "$lease_json" | json 'd["lease"]')
session=$(printf '%s' "$lease_json" | json 'd["session"]')
[ "$session" = cursor.s1 ] || fail "the session of the lease is $session"
echo "ok: POST /api/leases gives $session"
agents=$(curl -fsS "$base/api/agents" -H "authorization: Bearer $owner")
[ "$(printf '%s' "$agents" | json 'd["agents"][0]["leases"]')" = 1 ] || fail "GET /api/agents"
sessions=$(curl -fsS "$base/api/sessions" -H "authorization: Bearer $owner")
[ "$(printf '%s' "$sessions" | json 'd["sessions"][0]["leased"]')" = True ] || fail "GET /api/sessions"
echo "ok: GET /api/agents and GET /api/sessions show the lease"

beat=$(curl -fsS -X POST "$base/api/leases/current/heartbeat" -H "authorization: Bearer $lease")
[ "$(printf '%s' "$beat" | json 'd["session"]')" = cursor.s1 ] || fail "heartbeat"
echo "ok: heartbeat"

# call_mcp sets code to the HTTP status and body to the answer.
call_mcp() {
  out=$(curl -sS -w '\n%{http_code}' -X POST "$base/mcp" \
    -H 'content-type: application/json' -H 'accept: application/json, text/event-stream' "$@")
  code=$(printf '%s' "$out" | tail -n 1)
  body=$(printf '%s' "$out" | sed '$d')
}
call_mcp -H "authorization: Bearer $lease" -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
[ "$code" = 200 ] || fail "tools/list gave HTTP $code"
printf '%s' "$body" | python3 -c 'import json,sys; d=json.load(sys.stdin); n=[t["name"] for t in d["result"]["tools"]]; assert "login" not in n and "whoami" in n' ||
  fail "tools/list"
echo "ok: tools/list with the lease"
call_mcp -H "authorization: Bearer $lease" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 200 ] || fail "whoami gave HTTP $code"
printf '%s' "$body" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["result"]["structuredContent"]["login"] == "cursor.s1"' ||
  fail "whoami"
echo "ok: whoami gives cursor.s1"
call_mcp -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 401 ] || fail "a call with no lease gave HTTP $code"
echo "ok: a tool call with no lease gives 401"

code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$base/api/leases/current" -H "authorization: Bearer $lease")
[ "$code" = 204 ] || fail "DELETE lease gave HTTP $code"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$base/api/leases/current/heartbeat" -H "authorization: Bearer $lease")
[ "$code" = 401 ] || fail "a heartbeat after the end gave HTTP $code"
echo "ok: the lease ended"
echo "PASS"

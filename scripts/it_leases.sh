#!/bin/sh
# Start a mai container, set the owner key, add an agent, and test the lease
# API, MCP with a lease, the named sessions of the owner API and of the server
# CLI, the refusal of a pool label, and a pool lease with no session when the
# pool is full.
# Usage: scripts/it_leases.sh [IMAGE]   (default $MAI_IT_IMAGE, else mai:wf-leases)
# MAI_IT_NAME, MAI_IT_VOLUME, and MAI_IT_PORT set the names of the container
# and the volume, and the host port.
# It removes the container and its volumes at the end. It never pulls an image.
# It never prints a key or the body of an answer.
set -eu
image=${1:-${MAI_IT_IMAGE:-mai:wf-leases}}
name=${MAI_IT_NAME:-mai-wf-leases-it}
volume=${MAI_IT_VOLUME:-mai-wf-leases-data}
port=${MAI_IT_PORT:-18775}
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
[ "$(printf '%s' "$version" | json 'd["contract"]')" = 3.1.0 ] || fail "contract version"
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
printf '%s' "$body" | python3 -c '
import json, sys
tools = {t["name"]: t for t in json.load(sys.stdin)["result"]["tools"]}
assert {"login", "logout", "whoami"} <= set(tools)
assert tools["login"]["inputSchema"]["required"] == ["session"]
for name, t in tools.items():
    if name not in ("login", "logout"):
        session = t["inputSchema"]["properties"]["session"]
        assert session["pattern"] == "^ses-[a-z2-7]{16}$", name
        assert "session" not in t["inputSchema"].get("required", []), name
' || fail "tools/list"
echo "ok: tools/list has login, logout, and the argument session"
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

# Named sessions. Each answer of the owner API goes to owner_answers, and a
# check at the end finds a secret in any of them.
owner_answers=""
# owner_call METHOD PATH [BODY] sets code and body for a call of the owner API.
owner_call() {
  if [ $# -ge 3 ]; then
    out=$(curl -sS -w '\n%{http_code}' -X "$1" "$base$2" -H "authorization: Bearer $owner" \
      -H 'content-type: application/json' -d "$3")
  else
    out=$(curl -sS -w '\n%{http_code}' -X "$1" "$base$2" -H "authorization: Bearer $owner")
  fi
  code=$(printf '%s' "$out" | tail -n 1)
  body=$(printf '%s' "$out" | sed '$d')
  owner_answers="$owner_answers
$body"
}
# lease_call KEY BODY sets code and body for a lease request.
lease_call() {
  out=$(curl -sS -w '\n%{http_code}' -X POST "$base/api/leases" -H "authorization: Bearer $1" \
    -H 'content-type: application/json' -d "$2")
  code=$(printf '%s' "$out" | tail -n 1)
  body=$(printf '%s' "$out" | sed '$d')
}
# expect STATUS ERROR WHAT stops the test when the status or the error code is not correct.
expect() {
  [ "$code" = "$1" ] || fail "$3 gave HTTP $code, not $1"
  if [ -n "$2" ]; then
    got=$(printf '%s' "$body" | json 'd["error"]["code"]')
    [ "$got" = "$2" ] || fail "$3 gave $got, not $2"
  fi
}
# is_true EXPRESSION WHAT stops the test when the expression on the JSON body d is false.
is_true() {
  [ "$(printf '%s' "$body" | json "$1")" = True ] || fail "$2"
}

for label in lane-a lane-b; do
  owner_call POST /api/sessions "{\"agent\":\"cursor\",\"label\":\"$label\"}"
  expect 201 "" "session new $label"
  is_true "d == {'login': 'cursor.$label', 'agent': 'cursor', 'label': '$label', 'status': 'active'}" \
    "the answer of session new $label"
done
echo "ok: POST /api/sessions makes cursor.lane-a and cursor.lane-b"
owner_call POST /api/sessions '{"agent":"cursor","label":"lane-a"}'
expect 409 SESSION_EXISTS "a second session new lane-a"
owner_call POST /api/sessions '{"agent":"cursor","label":"Lane A"}'
expect 400 BAD_LABEL "session new with a bad label"
owner_call POST /api/sessions '{"agent":"nobody","label":"lane-a"}'
expect 404 AGENT_UNKNOWN "session new for an unknown agent"
echo "ok: SESSION_EXISTS, BAD_LABEL, and AGENT_UNKNOWN"
owner_call POST /api/sessions '{"agent":"cursor","count":2}'
expect 201 "" "session new with a count"
is_true "[s['login'] for s in d['sessions']] == ['cursor.s2']" "session new with a count"
echo "ok: a count makes only the pool sessions that do not exist"

lease_call "$agent" '{"app":"it","folder":"/it/a","pid":2,"session":"lane-a"}'
expect 201 "" "the lease of lane-a"
is_true "d['session'] == 'cursor.lane-a' and set(d) == {'lease', 'session', 'heartbeat_seconds', 'expires_seconds'}" \
  "the lease of lane-a"
lane_a=$(printf '%s' "$body" | json 'd["lease"]')
echo "ok: a lease of lane-a by name gives cursor.lane-a"
lease_call "$agent" '{"app":"it","folder":"/it/b","pid":3,"session":"lane-a"}'
expect 409 SESSION_BUSY "a second lease of lane-a"
lease_call "$agent" '{"app":"it","folder":"/it/b","pid":3,"session":"cursor.lane-a"}'
expect 409 SESSION_BUSY "a second lease of cursor.lane-a"
echo "ok: a second lease of lane-a gives SESSION_BUSY"
lease_call "$agent" '{"app":"it","folder":"/it/b","pid":3,"session":"lane-z"}'
expect 404 SESSION_UNKNOWN "a lease of a session that does not exist"
codex=$(curl -fsS -X POST "$base/api/agents" -H "authorization: Bearer $owner" \
  -H 'content-type: application/json' -d '{"name":"codex"}' | json 'd["agent_key"]')
lease_call "$codex" '{"app":"it","folder":"/it/c","pid":4,"session":"cursor.lane-b"}'
expect 404 SESSION_UNKNOWN "a lease of a session of a different agent"
echo "ok: a session of a different agent gives SESSION_UNKNOWN"
lease_call "$agent" '{"app":"it","folder":"/it/p","pid":5}'
expect 201 "" "a pool lease"
is_true "d['session'] in ('cursor.s1', 'cursor.s2')" "a pool lease gave a named session"
pool=$(printf '%s' "$body" | json 'd["lease"]')
echo "ok: a pool lease gives a pool session, not a named session"

call_mcp -H "authorization: Bearer $lane_a" \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 200 ] || fail "whoami of lane-a gave HTTP $code"
is_true "d['result']['structuredContent']['login'] == 'cursor.lane-a'" "whoami of lane-a"
echo "ok: whoami with the lease of lane-a gives cursor.lane-a"
call_mcp -H "authorization: Bearer $lane_a" \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"whoami","arguments":{"session":"ses-abcdefghijklmnop"}}}'
[ "$code" = 200 ] || fail "whoami with a session argument gave HTTP $code"
is_true "d['result']['structuredContent']['login'] == 'cursor.lane-a'" "whoami with a session argument"
echo "ok: the server removes the argument session"
for call in '"login","arguments":{"session":"lane-a"}' '"logout","arguments":{"session":"ses-abcdefghijklmnop"}'; do
  call_mcp -H "authorization: Bearer $lane_a" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"tools/call\",\"params\":{\"name\":$call}}"
  [ "$code" = 200 ] || fail "a call of a bridge tool gave HTTP $code"
  is_true "d['result']['isError'] is True and d['result']['structuredContent']['error']['code'] == 'BRIDGE_ONLY'" \
    "a call of a bridge tool"
done
echo "ok: login and logout on the server give BRIDGE_ONLY"

owner_call GET /api/sessions
expect 200 "" "GET /api/sessions"
is_true "{s['login']: (s['status'], s['leased'], s['app'], s['folder'], s['pid']) for s in d['sessions']}['cursor.lane-a'] == ('active', True, 'it', '/it/a', 2)" \
  "GET /api/sessions for cursor.lane-a"
is_true "[s for s in d['sessions'] if s['login'] == 'cursor.lane-b'][0]['leased'] is False" \
  "GET /api/sessions for cursor.lane-b"
is_true "all(set(s) == {'login', 'agent', 'label', 'status', 'leased', 'app', 'folder', 'pid', 'last_seen_at'} for s in d['sessions'])" \
  "the fields of GET /api/sessions"
echo "ok: GET /api/sessions shows the lease of cursor.lane-a"

owner_call POST /api/sessions/cursor.lane-b/revoke
expect 200 "" "revoke of lane-b"
is_true "d == {'login': 'cursor.lane-b', 'status': 'revoked'}" "the answer of revoke"
lease_call "$agent" '{"app":"it","folder":"/it/b","pid":6,"session":"lane-b"}'
expect 409 SESSION_REVOKED "a lease of lane-b after the revoke"
owner_call POST /api/sessions/cursor.lane-b/rotate
expect 409 SESSION_REVOKED "a rotate of lane-b after the revoke"
echo "ok: after the revoke of lane-b, a lease gives SESSION_REVOKED"

owner_call POST /api/sessions/cursor.lane-a/rotate
expect 200 "" "rotate of lane-a"
is_true "d == {'login': 'cursor.lane-a', 'status': 'active'}" "the answer of rotate"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$base/api/leases/current/heartbeat" -H "authorization: Bearer $lane_a")
[ "$code" = 401 ] || fail "a heartbeat of lane-a after the rotate gave HTTP $code"
call_mcp -H "authorization: Bearer $lane_a" \
  -d '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 401 ] || fail "a tool call of lane-a after the rotate gave HTTP $code"
echo "ok: the rotate of lane-a ends its lease"
lease_call "$agent" '{"app":"it","folder":"/it/a","pid":7,"session":"lane-a"}'
expect 201 "" "a new lease of lane-a after the rotate"
owner_call DELETE /api/sessions/cursor.lane-a/lease
expect 204 "" "DELETE /api/sessions/cursor.lane-a/lease"
owner_call DELETE /api/sessions/cursor.lane-a/lease
expect 404 "" "a second DELETE of the lease of lane-a"
echo "ok: a new lease of lane-a, and the owner ends it"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$base/api/leases/current" -H "authorization: Bearer $pool")
[ "$code" = 204 ] || fail "the end of the pool lease gave HTTP $code"

# The server CLI: each command shows only the login and the status.
cli=$(docker exec "$name" mai session new cursor --label lane-c)
[ "$cli" = cursor.lane-c ] || fail "the server CLI session new"
owner_answers="$owner_answers
$cli
$(docker exec "$name" mai session list --json)
$(docker exec "$name" mai session list)
$(docker exec "$name" mai session rotate cursor.lane-c)
$(docker exec "$name" mai session revoke cursor.lane-c)"
set +e
docker exec "$name" mai session new cursor --label lane-c >/dev/null 2>&1
code=$?
docker exec "$name" mai session new cursor --label lane-x --file /data/x.cred >/dev/null 2>&1
flag=$?
set -e
[ "$code" = 1 ] || fail "a second server CLI session new gave exit $code, not 1"
[ "$flag" = 2 ] || fail "the server CLI accepted --file"
owner_call GET /api/sessions
is_true "[s['status'] for s in d['sessions'] if s['login'] == 'cursor.lane-c'] == ['revoked']" \
  "the server CLI revoke"
echo "ok: the server CLI session new, list, rotate, and revoke"

# A label of the pool is not the label of a named session.
owner_call POST /api/sessions '{"agent":"cursor","label":"s1"}'
expect 400 BAD_LABEL "session new with the pool label s1"
is_true "'--count' in d['error']['message']" "the message of BAD_LABEL for s1"
set +e
refused=$(docker exec "$name" mai session new cursor --label s1 2>&1 >/dev/null)
code=$?
set -e
[ "$code" = 1 ] || fail "the server CLI session new --label s1 gave exit $code, not 1"
case "$refused" in *BAD_LABEL*--count*) ;; *) fail "the server CLI did not refuse the label s1" ;; esac
echo "ok: the owner API and the server CLI refuse the label s1"
for session in s1 cursor.s1; do
  lease_call "$agent" "{\"app\":\"it\",\"folder\":\"/it/l\",\"pid\":8,\"session\":\"$session\"}"
  expect 400 BAD_LABEL "a lease request for $session"
  is_true "'pool' in d['error']['message']" "the message of BAD_LABEL for $session"
done
echo "ok: a lease request for s1 or cursor.s1 gives BAD_LABEL"

# A full pool: 8 pool leases, then a lease with no session.
pool_tokens=""
third=""
for pid in 11 12 13 14 15 16 17 18; do
  lease_call "$agent" "{\"app\":\"it\",\"folder\":\"/it/pool\",\"pid\":$pid}"
  expect 201 "" "pool lease $pid"
  got=$(printf '%s' "$body" | json 'd["session"]')
  token=$(printf '%s' "$body" | json 'd["lease"]')
  pool_tokens="$pool_tokens $token"
  [ "$got" = cursor.s3 ] && third=$token
done
[ -n "$third" ] || fail "no pool lease got cursor.s3"
echo "ok: 8 pool leases take cursor.s1 to cursor.s8"
lease_call "$agent" '{"app":"it","folder":"/it/extra","pid":19}'
expect 201 "" "the 9th pool lease"
is_true "d['session'] is None and set(d) == {'lease', 'session', 'heartbeat_seconds', 'expires_seconds'}" \
  "the 9th pool lease has a session"
extra=$(printf '%s' "$body" | json 'd["lease"]')
echo "ok: the 9th pool lease gives the session null"
beat=$(curl -fsS -X POST "$base/api/leases/current/heartbeat" -H "authorization: Bearer $extra")
[ "$(printf '%s' "$beat" | json 'd == {"session": None, "new_mail": 0}')" = True ] ||
  fail "the heartbeat of the lease with no session"
leases=$(curl -fsS "$base/api/leases" -H "authorization: Bearer $owner")
[ "$(printf '%s' "$leases" | json '[l["pid"] for l in d["leases"] if l["session"] is None]')" = "[19]" ] ||
  fail "GET /api/leases does not show the lease with no session"
owner_call GET /api/sessions
is_true "None not in [s['login'] for s in d['sessions']]" "GET /api/sessions has a row with no session"
echo "ok: the heartbeat and GET /api/leases give the session null"
call_mcp -H "authorization: Bearer $extra" -d '{"jsonrpc":"2.0","id":8,"method":"tools/list"}'
[ "$code" = 200 ] || fail "tools/list on the lease with no session gave HTTP $code"
is_true "len(d['result']['tools']) == 22" "tools/list on the lease with no session"
echo "ok: tools/list works on the lease with no session"
call_mcp -H "authorization: Bearer $extra" \
  -d '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 200 ] || fail "whoami on the lease with no session gave HTTP $code"
is_true "d['result']['isError'] is True and d['result']['structuredContent']['error']['code'] == 'NO_FREE_SESSION' and d['result']['structuredContent']['error']['retry_safe'] is True" \
  "whoami with a full pool"
echo "ok: whoami with a full pool gives NO_FREE_SESSION with HTTP 200"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$base/api/leases/current" -H "authorization: Bearer $third")
[ "$code" = 204 ] || fail "the end of the lease of cursor.s3 gave HTTP $code"
call_mcp -H "authorization: Bearer $extra" \
  -d '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
[ "$code" = 200 ] || fail "whoami after the end of a pool lease gave HTTP $code"
is_true "d['result']['structuredContent']['login'] == 'cursor.s3'" "whoami after the end of a pool lease"
beat=$(curl -fsS -X POST "$base/api/leases/current/heartbeat" -H "authorization: Bearer $extra")
[ "$(printf '%s' "$beat" | json 'd["session"]')" = cursor.s3 ] || fail "the heartbeat after the bind"
echo "ok: after the end of a pool lease, whoami and the heartbeat give cursor.s3"
for token in $pool_tokens $extra; do
  [ "$token" = "$third" ] && continue
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$base/api/leases/current" -H "authorization: Bearer $token")
  [ "$code" = 204 ] || fail "the end of a pool lease gave HTTP $code"
done
leases=$(curl -fsS "$base/api/leases" -H "authorization: Bearer $owner")
[ "$(printf '%s' "$leases" | json 'len(d["leases"])')" = 0 ] || fail "a lease stays after the end"
echo "ok: the end of each lease"

printf '%s' "$owner_answers" | python3 -c '
import re, sys
text = sys.stdin.read()
assert "mai1:" not in text and "credential" not in text and "secret" not in text
assert not re.search(r"[a-z2-7]{40,}", text)
' || fail "an owner answer holds a secret"
echo "ok: no owner answer holds a secret"
echo "PASS"

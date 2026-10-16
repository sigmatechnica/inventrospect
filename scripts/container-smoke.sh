#!/usr/bin/env bash
# Uses an isolated Compose project; never touches the default installation.
set -euo pipefail
export COMPOSE_FILE=${COMPOSE_FILE:-compose.yaml:compose.build.yaml}
export INVENTROSPECT_IMAGE=${INVENTROSPECT_IMAGE:-inventrospect:local}
project="inventrospect-ci-${RANDOM}-${RANDOM}"
export HTTP_PORT=18080 MCP_PORT=18081
compose=(docker compose -p "$project")
cleanup() { "${compose[@]}" down --volumes --remove-orphans; }
trap cleanup EXIT
password=$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')
printf '%s\n' "$password" | "${compose[@]}" run -T --rm --no-deps app -data=/data -init-admin -admin-password-stdin
"${compose[@]}" up -d --wait --wait-timeout 120
[[ $(docker inspect --format '{{.Config.User}}' "$("${compose[@]}" ps -q app)") == 65532:65532 ]]
# Internal health can succeed even when Docker never publishes a host port.
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:18080/readyz >/dev/null
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:18081/readyz >/dev/null
"${compose[@]}" exec -T app inventrospect-ops health
"${compose[@]}" exec -T mcp inventrospect-ops health -url=http://127.0.0.1:8081/readyz
# Authentication rejects invalid MCP bearer tokens.
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer invalid' http://127.0.0.1:18081/mcp)
[[ "$code" == 401 ]]
printf '%s\n' "$password" | python3 scripts/http-smoke.py
"${compose[@]}" down
"${compose[@]}" up -d --wait --wait-timeout 120
printf '%s\n' "$password" | python3 scripts/http-smoke.py --verify
"${compose[@]}" stop
"${compose[@]}" run --rm backup backup -archive=/backups/smoke.tar.gz
"${compose[@]}" run --rm backup verify -archive=/backups/smoke.tar.gz
# Restore into a genuinely fresh volume, while retaining the source volume.
restore_volume="${project}_restore"
docker volume create "$restore_volume" >/dev/null
trap 'docker volume rm "$restore_volume"; cleanup' EXIT
docker run --rm --network none -v "${project}_backups:/backups" -v "$restore_volume:/data" \
  --entrypoint /usr/local/bin/inventrospect-ops "${INVENTROSPECT_IMAGE:-inventrospect:local}" restore -archive=/backups/smoke.tar.gz
restore_container="${project}-restored"
trap 'docker rm -f "$restore_container" >/dev/null 2>&1 || true; docker volume rm "$restore_volume"; cleanup' EXIT
docker run -d --name "$restore_container" -p 127.0.0.1:18082:8080 -v "$restore_volume:/data" \
  "${INVENTROSPECT_IMAGE:-inventrospect:local}" -data=/data -addr=0.0.0.0:8080 -allow-insecure-http >/dev/null
for attempt in {1..30}; do
  if curl --fail --silent http://127.0.0.1:18082/readyz >/dev/null; then break; fi
  sleep 1
done
printf '%s\n' "$password" | SMOKE_APP_URL=http://127.0.0.1:18082 python3 scripts/http-smoke.py --verify

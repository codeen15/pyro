#!/usr/bin/env bash
# Smoke-tests a Pyro image: the team server starts as non-root on a fresh volume,
# keeps its data across restarts, and admin commands work in the shell-less image.
#
#   scripts/docker-smoke.sh <image>
set -euo pipefail

image=${1:?usage: docker-smoke.sh <image>}
name=pyro-smoke-$$
vol=pyro-smoke-$$
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')

cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
	docker volume rm -f "$vol" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*"; }

start() {
	docker run -d --name "$name" -v "$vol:/data" -p "127.0.0.1:$port:7878" "$image" >/dev/null ||
		fail "could not start $image"
}

health() {
	for _ in $(seq 60); do
		if body=$(curl -fsS "http://127.0.0.1:$port/api/v1/health" 2>/dev/null); then
			echo "$body"
			return 0
		fi
		sleep 0.5
	done
	docker logs "$name" >&2 || true
	return 1
}

# Lists /data as the volume sees it, from a throwaway container.
volume() { docker run --rm -v "$vol:/data" busybox:1.36 "$@"; }

docker pull -q busybox:1.36 >/dev/null
start
body=$(health) || fail "no answer on /api/v1/health"
[[ $body == *'"mode":"team"'* ]] || fail "not in team mode: $body"
version=$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' <<<"$body")
[[ -n $version && $version != dev ]] || fail "version not stamped: $body"
ok "team server answers ($version)"

docker exec "$name" /pyro version >/dev/null || fail "/pyro version failed"
ok "/pyro version"

user=$(docker inspect -f '{{.Config.User}}' "$image")
[[ $user == nonroot || $user == 65532 || $user == 65532:65532 ]] || fail "runs as '$user', not nonroot"
listing=$(volume ls -ln /data)
for f in pyro.db jwt.secret; do
	grep -Eq "65532 +65532 .* $f\$" <<<"$listing" || fail "/data/$f missing or not owned by 65532:
$listing"
done
ok "runs as nonroot; /data/pyro.db and /data/jwt.secret written"

before=$(volume sha256sum /data/jwt.secret)
docker restart "$name" >/dev/null
health >/dev/null || fail "no answer after restart"
after=$(volume sha256sum /data/jwt.secret)
[[ $before == "$after" ]] || fail "jwt.secret changed across a restart"
ok "data survives a restart"

docker exec "$name" /pyro admin users | grep -q '^EMAIL' || fail "/pyro admin users failed"
ok "/pyro admin works against the server's data"

size=$(docker image inspect -f '{{.Size}}' "$image")
((size < 80 * 1024 * 1024)) || fail "image is $((size / 1024 / 1024)) MB, over 80 MB"
ok "image is $((size / 1024 / 1024)) MB"

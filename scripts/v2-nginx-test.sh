#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

fail() {
  printf 'v2-nginx-test: %s\n' "$*" >&2
  exit 1
}

for command in docker curl openssl awk grep cmp; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

NGINX_IMAGE="${GINBAR_NGINX_TEST_IMAGE:-nginx:1.27-alpine}"
NODE_IMAGE="${GINBAR_NODE_TEST_IMAGE:-node:22.20.0-bookworm}"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ginbar-v2-nginx.XXXXXX")"
API_CONTAINER="ginbar-v2-nginx-api-$$"
NGINX_CONTAINER="ginbar-v2-nginx-$$"

cleanup() {
  local status=$?
  trap - EXIT
  docker rm -f "$NGINX_CONTAINER" "$API_CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP_ROOT" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

ensure_image() {
  local image="$1"
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker pull "$image" >/dev/null
  fi
}

ensure_image "$NGINX_IMAGE"
ensure_image "$NODE_IMAGE"

FRONTEND_DIR="$TMP_ROOT/frontend"
MEDIA_ROOT="$TMP_ROOT/media-root"
TLS_DIR="$TMP_ROOT/tls"
mkdir -p "$FRONTEND_DIR" "$MEDIA_ROOT/media/00/1" "$MEDIA_ROOT/sources/aa" "$TLS_DIR"

printf '0123456789abcdef' >"$MEDIA_ROOT/media/00/1/v1-fixture.avif"
printf 'private-source' >"$MEDIA_ROOT/sources/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=ginbar.test' \
  -keyout "$TLS_DIR/tls.key" \
  -out "$TLS_DIR/tls.crt" >/dev/null 2>&1

docker run --rm \
  -v "$ROOT/src/frontend:/src:ro" \
  -v "$FRONTEND_DIR:/out" \
  "$NODE_IMAGE" \
  bash -lc 'cp -a /src /tmp/frontend && cd /tmp/frontend && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null && cp -a dist/. /out/'

[[ -f "$FRONTEND_DIR/index.html" ]] || fail "Vite build did not produce index.html"
ASSET_PATH="$(grep -oE '/assets/[^"[:space:]]+\.js' "$FRONTEND_DIR/index.html" | head -n 1 || true)"
[[ -n "$ASSET_PATH" && -f "$FRONTEND_DIR$ASSET_PATH" ]] || fail "could not locate built hashed Vite asset"

cat >"$TMP_ROOT/api-fixture.mjs" <<'EOF_API'
import http from "node:http";

function reply(req, res, bytes) {
  res.statusCode = 200;
  res.setHeader("Content-Type", "application/json");
  res.setHeader("Cache-Control", "no-store");
  res.end(JSON.stringify({
    method: req.method,
    url: req.url,
    host: req.headers.host ?? "",
    forwardedProto: req.headers["x-forwarded-proto"] ?? "",
    bytes,
  }));
}

let streamProbeSeen = false;
let authUpstreamCount = 0;

const server = http.createServer((req, res) => {
  if (req.url === "/fixture-auth-count") {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ count: authUpstreamCount }));
    return;
  }
  if (req.method === "POST" && (req.url === "/api/v2/auth/login" || req.url === "/api/v2/auth/register")) {
    authUpstreamCount++;
  }
  if (req.url === "/stream-probe") {
    res.statusCode = 200;
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ seen: streamProbeSeen }));
    return;
  }
  if (req.headers["x-fixture-stream-probe"] === "1") {
    streamProbeSeen = true;
  }

  let bytes = 0;
  req.on("data", (chunk) => {
    bytes += chunk.length;
  });
  req.on("end", () => reply(req, res, bytes));
});

server.listen(8080, "0.0.0.0");
EOF_API

docker run -d --rm \
  --name "$API_CONTAINER" \
  --publish 127.0.0.1::80/tcp \
  --publish 127.0.0.1::443/tcp \
  -v "$TMP_ROOT/api-fixture.mjs:/fixture.mjs:ro" \
  "$NODE_IMAGE" node /fixture.mjs >/dev/null

for _ in $(seq 1 40); do
  if docker exec "$API_CONTAINER" node -e 'fetch("http://127.0.0.1:8080/ready").then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))' >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
docker exec "$API_CONTAINER" node -e 'fetch("http://127.0.0.1:8080/ready").then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))' >/dev/null 2>&1 \
  || fail "API fixture did not become ready"

HTTP_PORT="$(docker port "$API_CONTAINER" 80/tcp | awk -F: 'NR == 1 { print $NF }')"
HTTPS_PORT="$(docker port "$API_CONTAINER" 443/tcp | awk -F: 'NR == 1 { print $NF }')"
[[ -n "$HTTP_PORT" && -n "$HTTPS_PORT" ]] || fail "could not resolve published fixture ports"

NGINX_MOUNTS=(
  -v "$ROOT/nginx/v2/nginx.conf:/etc/nginx/nginx.conf:ro"
  -v "$FRONTEND_DIR:/srv/ginbar/frontend:ro"
  -v "$MEDIA_ROOT:/srv/ginbar/media:ro"
  -v "$TLS_DIR:/etc/nginx/tls:ro"
)

docker run --rm \
  --network "container:$API_CONTAINER" \
  "${NGINX_MOUNTS[@]}" \
  "$NGINX_IMAGE" nginx -T >"$TMP_ROOT/nginx-T.txt" 2>&1 \
  || {
    cat "$TMP_ROOT/nginx-T.txt" >&2
    fail "nginx configuration failed to load"
  }

grep -Fq 'client_max_body_size 257m;' "$TMP_ROOT/nginx-T.txt" || fail "upload body limit is not loaded"
grep -Fq 'proxy_request_buffering off;' "$TMP_ROOT/nginx-T.txt" || fail "upload streaming directive is not loaded"
grep -Fq 'proxy_read_timeout 135s;' "$TMP_ROOT/nginx-T.txt" || fail "ingest proxy timeout is not loaded"
grep -Fq 'limit_req_zone $binary_remote_addr zone=ginbar_v2_auth:10m rate=2r/s;' "$TMP_ROOT/nginx-T.txt" || fail "auth rate zone missing"
grep -Fq 'limit_req zone=ginbar_v2_auth burst=10 nodelay;' "$TMP_ROOT/nginx-T.txt" || fail "auth burst policy missing"
grep -Fq 'limit_req_status 429;' "$TMP_ROOT/nginx-T.txt" || fail "rate rejection status missing"

docker run -d --rm \
  --name "$NGINX_CONTAINER" \
  --network "container:$API_CONTAINER" \
  "${NGINX_MOUNTS[@]}" \
  "$NGINX_IMAGE" nginx -g 'daemon off;' >/dev/null

HTTPS_URL="https://ginbar.test:$HTTPS_PORT"
HTTP_URL="http://ginbar.test:$HTTP_PORT"
CURL_HTTPS=(curl --silent --show-error --insecure --resolve "ginbar.test:$HTTPS_PORT:127.0.0.1")
CURL_HTTP=(curl --silent --show-error --resolve "ginbar.test:$HTTP_PORT:127.0.0.1")

for _ in $(seq 1 40); do
  status="$("${CURL_HTTPS[@]}" -o /dev/null -w '%{http_code}' "$HTTPS_URL/" 2>/dev/null || true)"
  [[ "$status" == "200" ]] && break
  sleep 0.1
done
[[ "${status:-}" == "200" ]] || fail "nginx HTTPS fixture did not become ready"

redirect_headers="$TMP_ROOT/redirect.headers"
"${CURL_HTTP[@]}" -D "$redirect_headers" -o /dev/null "$HTTP_URL/post/123"
grep -Eq '^HTTP/1\.[01] 308' "$redirect_headers" || fail "HTTP listener did not redirect to TLS"

spa_headers="$TMP_ROOT/spa.headers"
spa_body="$TMP_ROOT/spa.body"
"${CURL_HTTPS[@]}" -D "$spa_headers" -o "$spa_body" "$HTTPS_URL/post/123"
cmp -s "$FRONTEND_DIR/index.html" "$spa_body" || fail "canonical SPA route did not fall back to index.html"
grep -Fqi 'Cache-Control: no-store' "$spa_headers" || fail "SPA shell is missing no-store"

asset_headers="$TMP_ROOT/asset.headers"
"${CURL_HTTPS[@]}" -D "$asset_headers" -o /dev/null "$HTTPS_URL$ASSET_PATH"
grep -Fqi 'Cache-Control: public, max-age=31536000, immutable' "$asset_headers" \
  || fail "hashed Vite asset is missing immutable cache policy"

media_headers="$TMP_ROOT/media.headers"
media_body="$TMP_ROOT/media.body"
"${CURL_HTTPS[@]}" -D "$media_headers" -o "$media_body" "$HTTPS_URL/media/00/1/v1-fixture.avif"
[[ "$(cat "$media_body")" == '0123456789abcdef' ]] || fail "processed media was not served"
grep -Fqi 'Cache-Control: public, max-age=31536000, immutable' "$media_headers" \
  || fail "processed media is missing immutable cache policy"

range_headers="$TMP_ROOT/range.headers"
range_body="$TMP_ROOT/range.body"
range_status="$("${CURL_HTTPS[@]}" -H 'Range: bytes=2-5' -D "$range_headers" -o "$range_body" -w '%{http_code}' "$HTTPS_URL/media/00/1/v1-fixture.avif")"
[[ "$range_status" == "206" ]] || fail "processed media range request did not return 206"
[[ "$(cat "$range_body")" == '2345' ]] || fail "processed media range body is incorrect"
grep -Fqi 'Content-Range: bytes 2-5/16' "$range_headers" || fail "processed media Content-Range is incorrect"

source_status="$("${CURL_HTTPS[@]}" --path-as-is -o /dev/null -w '%{http_code}' "$HTTPS_URL/sources/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")"
[[ "$source_status" == "404" ]] || fail "ingestion source path is externally reachable"
traversal_status="$("${CURL_HTTPS[@]}" --path-as-is -o /dev/null -w '%{http_code}' "$HTTPS_URL/media/../sources/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")"
[[ "$traversal_status" == "404" ]] || fail "media path traversal reached ingestion sources"

api_body="$TMP_ROOT/api.body"
"${CURL_HTTPS[@]}" \
  -H "Origin: https://ginbar.test:$HTTPS_PORT" \
  -H 'Content-Type: application/json' \
  --data-binary '{"username":"fixture","password":"fixture"}' \
  -o "$api_body" \
  "$HTTPS_URL/api/v2/auth/login"
grep -Fq "\"host\":\"ginbar.test:$HTTPS_PORT\"" "$api_body" || fail "proxy did not preserve Host including explicit port"
grep -Fq '"forwardedProto":"https"' "$api_body" || fail "proxy did not propagate X-Forwarded-Proto=https"

# nginx must overwrite client-supplied forwarded scheme with its own TLS state,
# including malformed/chained input, while preserving the explicit Host port.
"${CURL_HTTPS[@]}" \
  -H 'X-Forwarded-Proto: http, https' \
  -H "Origin: https://ginbar.test:$HTTPS_PORT" \
  -H 'Content-Type: application/json' \
  --data-binary '{"username":"fixture","password":"fixture"}' \
  -o "$api_body" \
  "$HTTPS_URL/api/v2/auth/login"
grep -Fq '"forwardedProto":"https"' "$api_body" || fail "nginx forwarded a client-supplied scheme"

# Duplicate client-supplied forwarding fields must also be replaced.
"${CURL_HTTPS[@]}" \
  -H 'X-Forwarded-Proto: https' \
  -H 'X-Forwarded-Proto: http' \
  -H 'Content-Type: application/json' \
  --data-binary '{"username":"fixture","password":"fixture"}' \
  -o "$api_body" \
  "$HTTPS_URL/api/v2/auth/login"
grep -Fq '"forwardedProto":"https"' "$api_body" || fail "nginx forwarded duplicate client scheme headers"
grep -Fq "\"host\":\"ginbar.test:$HTTPS_PORT\"" "$api_body" || fail "nginx rewrote Host while sanitizing forwarding header"
grep -Fq '"url":"/api/v2/auth/login"' "$api_body" || fail "API path was not preserved"

small_upload="$TMP_ROOT/small-upload.bin"
head -c 1048576 /dev/zero >"$small_upload"
small_upload_status="$("${CURL_HTTPS[@]}" \
  -H "Origin: https://ginbar.test:$HTTPS_PORT" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@$small_upload" \
  -o /dev/null -w '%{http_code}' \
  "$HTTPS_URL/api/v2/posts/upload?filter=sfw")"
[[ "$small_upload_status" == "200" ]] || fail "bounded upload body was not proxied"

oversize_upload_status="$("${CURL_HTTPS[@]}" \
  --max-time 5 \
  -H 'Expect:' \
  -H 'Content-Length: 269484033' \
  --request POST --data-binary '' \
  -o /dev/null -w '%{http_code}' \
  "$HTTPS_URL/api/v2/posts/upload?filter=sfw" || true)"
[[ "$oversize_upload_status" == "413" ]] || fail "upload body above 257 MiB was not rejected at nginx"

oversize_url_status="$("${CURL_HTTPS[@]}" \
  --max-time 5 \
  -H 'Expect:' \
  -H 'Content-Length: 16385' \
  --request POST --data-binary '' \
  -o /dev/null -w '%{http_code}' \
  "$HTTPS_URL/api/v2/posts/import-url" || true)"
[[ "$oversize_url_status" == "413" ]] || fail "URL-ingestion body above 16 KiB was not rejected at nginx"

stream_log="$TMP_ROOT/stream-upload.log"
"${CURL_HTTPS[@]}" \
  --limit-rate 256k \
  -H 'Expect:' \
  -H 'X-Fixture-Stream-Probe: 1' \
  --data-binary "@$small_upload" \
  -o /dev/null \
  "$HTTPS_URL/api/v2/posts/upload?filter=sfw" >"$stream_log" 2>&1 &
STREAM_CURL_PID=$!

sleep 1
stream_probe="$(docker exec "$API_CONTAINER" node -e 'fetch("http://127.0.0.1:8080/stream-probe").then(r => r.text()).then(t => process.stdout.write(t)).catch(() => process.exit(1))')"
if ! grep -Fq '"seen":true' <<<"$stream_probe"; then
  kill "$STREAM_CURL_PID" >/dev/null 2>&1 || true
  wait "$STREAM_CURL_PID" 2>/dev/null || true
  fail "upload request was not delivered upstream while its slow body was still in progress"
fi
if ! wait "$STREAM_CURL_PID"; then
  cat "$stream_log" >&2
  fail "slow streaming upload did not complete"
fi


# Exercise the real TLS listener and upstream with a single TCP peer.
# Each auth URL is exact-matched, and both share one limit_req zone.
auth_count() {
  docker exec "$API_CONTAINER" node -e 'fetch("http://127.0.0.1:8080/fixture-auth-count").then(r => r.json()).then(v => process.stdout.write(String(v.count))).catch(() => process.exit(1))'
}
auth_post() {
  local route="$1"
  shift
  "${CURL_HTTPS[@]}" --max-time 5 -H 'Content-Type: application/json' "$@" \
    --data-binary '{"username":"fixture","password":"fixture"}' \
    -o /dev/null -w '%{http_code}' "$HTTPS_URL/api/v2/auth/$route"
}

# Existing auth probes consumed some initial budget; allow the zone to drain.
sleep 6
before_auth="$(auth_count)"
[[ "$(auth_post login)" == "200" ]] || fail "normal login did not reach Go"
[[ "$(auth_post register)" == "200" ]] || fail "normal registration did not reach Go"
[[ "$(auth_count)" == "$((before_auth + 2))" ]] || fail "normal auth requests did not reach upstream"

# A fresh budget permits ten excess requests without artificial delay.
sleep 6
before_burst="$(auth_count)"
for i in $(seq 1 10); do
  route=login
  if (( i % 2 == 0 )); then route=register; fi
  [[ "$(auth_post "$route")" == "200" ]] || fail "documented auth burst was rejected at request $i"
done
[[ "$(auth_count)" == "$((before_burst + 10))" ]] || fail "burst traffic did not all reach upstream"

# Repeated requests from the same TCP peer cannot reset the bucket by
# rotating untrusted X-Forwarded-For identities.
before_flood="$(auth_count)"
rejected=0
admitted=0
for i in $(seq 1 35); do
  code="$(auth_post login -H "X-Forwarded-For: 198.51.100.$i")"
  case "$code" in
    429) rejected=$((rejected + 1));;
    200) admitted=$((admitted + 1));;
    *) fail "unexpected flood response $code";;
  esac
done
(( rejected >= 1 )) || fail "forged X-Forwarded-For bypassed auth rate limit"
[[ "$(auth_count)" == "$((before_flood + admitted))" ]] || fail "rejected auth requests reached Go"

# Non-auth API requests are not in the auth zone during the flood.
non_auth_code="$("${CURL_HTTPS[@]}" -o /dev/null -w '%{http_code}' "$HTTPS_URL/api/v2/posts")"
[[ "$non_auth_code" == "200" ]] || fail "non-auth API route was throttled"

# Token-bucket capacity recovers without requiring a service restart.
sleep 6
[[ "$(auth_post login)" == "200" ]] || fail "auth limiter did not recover"
[[ "$(auth_post register)" == "200" ]] || fail "shared limiter did not recover for registration"

printf 'v2-nginx-test: PASS https_port=%s asset=%s streaming_upstream_seen=true\n' "$HTTPS_PORT" "$ASSET_PATH"

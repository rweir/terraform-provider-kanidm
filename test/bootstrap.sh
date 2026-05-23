#!/usr/bin/env bash
#
# Bring up the acceptance-test Kanidm and mint a fresh RW API token.
# Output:
#   - test/.env  (sourceable: KANIDM_URL, KANIDM_TOKEN, SSL_CERT_FILE)
#
# Idempotent in the sense that re-running wipes the database and starts
# over with a brand-new admin password and a fresh token. Use that
# behaviour deliberately: acceptance tests should never have to reason
# about cruft left over from prior runs.

set -euo pipefail
cd "$(dirname "$0")"

DATA="$(pwd)/data"
COMPOSE="docker compose"

mkdir -p "$DATA"

# --- Certs ----------------------------------------------------------------
# Self-signed cert covering localhost / 127.0.0.1 / ::1 with a 10-year
# expiry. Regenerated when missing; gitignored.
if true; then  # always regenerate — cheap, and avoids stale-cert puzzles
    echo ">>> generating self-signed cert"
    rm -f "$DATA/cert.pem" "$DATA/key.pem"
    openssl req -x509 -newkey rsa:2048 -nodes \
        -keyout "$DATA/key.pem" \
        -out "$DATA/cert.pem" \
        -days 3650 \
        -subj "/CN=idm.localhost" \
        -addext "subjectAltName=DNS:idm.localhost,DNS:localhost,IP:127.0.0.1,IP:::1" \
        -addext "keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign" \
        -addext "extendedKeyUsage=serverAuth,clientAuth" \
        -addext "basicConstraints=critical,CA:TRUE" \
        >/dev/null 2>&1
fi
# Kanidm runs as uid 389 in the official image; make the cert readable.
chmod 644 "$DATA/cert.pem" "$DATA/key.pem"

# --- Server config --------------------------------------------------------
cat > "$DATA/server.toml" <<'EOF'
version = "2"
domain = "idm.localhost"
origin = "https://idm.localhost:8443"
bindaddress = "[::]:8443"
tls_chain = "/data/cert.pem"
tls_key = "/data/key.pem"
db_path = "/data/kanidm.db"
log_level = "info"
EOF

# --- Fresh database -------------------------------------------------------
echo ">>> wiping prior state"
$COMPOSE down -v 2>/dev/null || true
rm -f "$DATA/kanidm.db" "$DATA/kanidm.db-wal" "$DATA/kanidm.db-shm"

echo ">>> starting kanidm"
$COMPOSE up -d
sleep 1

# Poll /status from the host until it responds (or we give up).
echo -n ">>> waiting for kanidm to respond"
ready=false
for _ in $(seq 1 60); do
    if curl -kfsS --max-time 2 https://127.0.0.1:8443/status >/dev/null 2>&1; then
        echo " ok"
        ready=true
        break
    fi
    echo -n "."
    sleep 1
done
if [[ "$ready" != true ]]; then
    echo
    echo "kanidm never started serving. Last container logs:"
    $COMPOSE logs --tail=80 kanidm
    exit 1
fi

# --- Recover idm_admin ----------------------------------------------------
# `kanidmd recover-account` prints either a JSON object or a textual
# "new password: ..." line, depending on version. Capture the full
# output and try both shapes. We recover `idm_admin` (not `admin`):
# admin is the system administrator; idm_admin holds the IDM-level
# privileges we need to create service accounts.
echo ">>> recovering idm_admin"
RECOVER_RAW=$(docker exec -i kanidm-acctest \
    kanidmd recover-account -c /data/server.toml idm_admin 2>&1 || true)

# Log line shape (kanidm 1.10): `... new_password: "<password>"`
# Older / JSON shapes: `"new_password":"..."` or `"password":"..."`
# Just look for `(new_)?password ... " <chars> "` regardless of wrapping.
ADMIN_PW=$(printf '%s\n' "$RECOVER_RAW" \
    | grep -oE '(new_password|password)[[:space:]]*:[[:space:]"]+[A-Za-z0-9]+' \
    | head -1 \
    | sed -E 's/.*[[:space:]"]([A-Za-z0-9]+)$/\1/' || true)

# Last-resort textual shape: `... password: XYZ` with no quotes.
if [[ -z "$ADMIN_PW" ]]; then
    ADMIN_PW=$(printf '%s\n' "$RECOVER_RAW" \
        | sed -nE 's/.*(new[[:space:]_]+)?password[[:space:]]*:[[:space:]]*"?([A-Za-z0-9]+)"?.*/\2/p' \
        | head -1 || true)
fi

if [[ -z "$ADMIN_PW" ]]; then
    echo
    echo "FATAL: could not parse admin password from recover-account output."
    echo "Raw output was:"
    printf '%s\n' "$RECOVER_RAW" | sed 's/^/    /'
    echo
    echo "To debug interactively:"
    echo "   docker exec -it kanidm-acctest kanidmd recover-account -c /data/server.toml admin"
    exit 1
fi
echo "    idm_admin password recovered (${#ADMIN_PW} chars)"

# --- Bootstrap a RW service account --------------------------------------
# kanidm/server is a minimal image with no shell or CLI bundled. Use
# the kanidm/tools image as a throwaway sidecar on the same Docker
# network, talking to the server by its compose service name.
#
# The cert covers localhost/127.0.0.1/idm.localhost — NOT the in-
# network DNS name `kanidm` — so we run with verify_ca=false. That's
# fine: the acceptance tests' threat model is "don't break".

# Discover the docker network that compose created (depends on the
# project name = test/ directory). Look up by inspecting the running
# container.
NETWORK=$(docker inspect -f '{{range $k, $_ := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' \
    kanidm-acctest | head -1)
if [[ -z "$NETWORK" ]]; then
    echo "FATAL: could not determine kanidm-acctest's docker network"
    exit 1
fi

echo ">>> grabbing idm_admin session token (via kanidm/tools on network $NETWORK)"

# Pull kanidm/tools explicitly first so its noisy `docker pull` output
# doesn't end up in BOOTSTRAP_RAW (which we parse for the token).
docker pull -q kanidm/tools:latest >/dev/null

# Why no service account: a freshly recover-account-ed credential can't
# reauth (Kanidm returns SessionMayNotReauth), so we can't add a new SA
# to idm_admins to give it privileges. Skipping the SA dance: the
# provider just uses idm_admin's session token directly. Sessions
# default to 24h TTL — plenty for one acceptance-test run.
BOOTSTRAP_RAW=$(docker run --rm -i \
    --network "$NETWORK" \
    -e KANIDM_PASSWORD="$ADMIN_PW" \
    --entrypoint sh \
    kanidm/tools:latest \
    -s <<'SCRIPT' 2>&1 || true
set -eu
mkdir -p ~/.config
cat > ~/.config/kanidm <<EOF
uri = "https://kanidm:8443"
verify_ca = false
EOF

# KANIDM_PASSWORD is consumed automatically by the login flow.
kanidm login --name idm_admin >&2

# After login, the kanidm CLI caches the session token at
# ~/.cache/kanidm_tokens as JSON like
#     {"idm_admin@idm.localhost":"eyJhbGc..."}
# Extract the token value with a minimal sed.
sed -E 's/.*"([A-Za-z0-9_.-]+)".*/\1/' ~/.cache/kanidm_tokens
SCRIPT
)

# Pick out the longest JWT-shaped token. (Both the session token and
# any debug log lines containing dotted things end up in BOOTSTRAP_RAW;
# the session token is by far the longest.)
TOKEN=$(printf '%s\n' "$BOOTSTRAP_RAW" \
    | grep -oE '[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}' \
    | awk '{ print length, $0 }' | sort -nr | head -1 | cut -d' ' -f2- \
    || true)

if [[ -z "$TOKEN" ]]; then
    echo
    echo "FATAL: could not extract idm_admin session token. Raw bootstrap output:"
    printf '%s\n' "$BOOTSTRAP_RAW" | sed 's/^/    /'
    exit 1
fi
echo "    session token extracted (${#TOKEN} chars)"

# --- Write .env -----------------------------------------------------------
cat > .env <<EOF
# Generated by test/bootstrap.sh. Source this from your shell to run
# acceptance tests against the local Kanidm.
export KANIDM_URL='https://localhost:8443'
export KANIDM_TOKEN='${TOKEN}'
# Trust the dev self-signed cert via the Go runtime's cert bundle override.
export SSL_CERT_FILE='$(pwd)/data/cert.pem'
# Also tell the provider to skip verification — belt + suspenders so
# the harness doesn't break if Go's x509 verifier gets stricter.
export KANIDM_INSECURE_SKIP_VERIFY=1
EOF
chmod 600 .env

cat <<EOF

>>> Done. To run the acceptance tests:

  source test/.env
  TF_ACC=1 go test -tags=acc -v ./internal/provider/...

Or via make:

  make test-acc

KANIDM_URL=https://localhost:8443
KANIDM_TOKEN=<written to test/.env>
EOF

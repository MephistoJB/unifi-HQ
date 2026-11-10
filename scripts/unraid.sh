#!/usr/bin/env bash
# UniFi HQ native Docker/Unraid lifecycle, no Compose plugin required.
# Modified fork 2026-11-10; GPL-3.0, see LICENSE.
set -euo pipefail
umask 077
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
ACTION=${1:-preview}
APPLY=${2:-}
HQ_VERSION=${HQ_VERSION:-0.1.0}
HQ_IMAGE_PREFIX=${HQ_IMAGE_PREFIX:-ghcr.io/mephistojb/unifi-hq}
HQ_DATA_DIR=${HQ_DATA_DIR:-/mnt/user/appdata/unifi-hq}
HQ_SECRETS_DIR=${HQ_SECRETS_DIR:-/run/unifi-hq-secrets}
HQ_TEMPLATE_DIR=${HQ_TEMPLATE_DIR:-/boot/config/plugins/dockerMan/templates-user}
HQ_ALLOW_UNCONFIGURED=${HQ_ALLOW_UNCONFIGURED:-0}
UNIFI_KEY_FILE=/run/secrets/unifi_api_key
HTTP_PORT=${HTTP_PORT:-8080}
IPFIX_PORT=${IPFIX_PORT:-2055}
SYSLOG_PORT=${SYSLOG_PORT:-5514}
LIVE_PORT=${LIVE_PORT:-8090}
METRICS_PORT=${METRICS_PORT:-8091}
API_PORT=${API_PORT:-8092}
HISTORY_PORT=${HISTORY_PORT:-9428}
TZ=${TZ:-Europe/Berlin}

fail() { printf 'UniFi HQ: %s\n' "$*" >&2; exit 1; }
run() { if [[ $APPLY == --apply ]]; then "$@"; else printf '%q ' "$@"; printf '\n'; fi; }
common=(--network host --restart unless-stopped --read-only --cap-drop ALL
  --security-opt no-new-privileges:true --log-driver local
  --log-opt max-size=5m --log-opt max-file=2
  --label io.unifi-hq.stack=unifi-hq --label net.unraid.docker.managed=dockerman)

validate_config() {
  [[ $HQ_ALLOW_UNCONFIGURED == 0 || $HQ_ALLOW_UNCONFIGURED == 1 ]] || fail 'HQ_ALLOW_UNCONFIGURED must be 0 or 1.'
  if [[ $HQ_ALLOW_UNCONFIGURED == 1 && -z ${UNIFI_URL:-} ]]; then UNIFI_KEY_FILE=; fi
  [[ $HQ_DATA_DIR == /* && $HQ_SECRETS_DIR == /* ]] || fail 'Data and secret paths must be absolute.'
  [[ $HQ_DATA_DIR != / && $HQ_SECRETS_DIR != / ]] || fail 'Refusing root mount.'
  local port seen=' '
  for port in "$HTTP_PORT" "$IPFIX_PORT" "$SYSLOG_PORT" "$LIVE_PORT" "$METRICS_PORT" "$API_PORT" "$HISTORY_PORT"; do
    [[ $port =~ ^[0-9]+$ ]] && ((port > 1024 && port <= 65535)) || fail "Invalid port: $port"
    [[ $seen != *" $port "* ]] || fail "Duplicate port: $port"
    seen+="$port "
  done
  [[ ${HOME_LAT:-50.11} =~ ^-?[0-9]+([.][0-9]+)?$ ]] || fail 'Invalid HOME_LAT.'
  [[ ${HOME_LON:-8.68} =~ ^-?[0-9]+([.][0-9]+)?$ ]] || fail 'Invalid HOME_LON.'
  [[ ${HOME_LABEL:-Home} != *'"'* && ${GATEWAY_NAME:-Gateway} != *'"'* ]] || fail 'Labels must not contain quotes.'
}

check_secrets() {
  local file perms
  [[ -d $HQ_SECRETS_DIR && ! -L $HQ_SECRETS_DIR ]] || fail 'Runtime secret directory missing or symlinked.'
  perms=$(stat -c '%a' "$HQ_SECRETS_DIR")
  [[ $perms == 700 ]] || fail 'Runtime secret directory must have mode 700.'
  local required=(auth_password_hash auth_secret)
  if [[ -z $UNIFI_KEY_FILE ]]; then
    [[ ! -e $HQ_SECRETS_DIR/unifi_api_key ]] || fail 'Unconfigured mode requires no controller key file; configure UNIFI_URL instead.'
  else
    required+=(unifi_api_key)
    [[ ${UNIFI_URL:-} == https://* ]] || fail 'Set UNIFI_URL to the HTTPS controller URL.'
  fi
  for file in "${required[@]}"; do
    [[ -f $HQ_SECRETS_DIR/$file && -s $HQ_SECRETS_DIR/$file && ! -L $HQ_SECRETS_DIR/$file ]] || fail "Missing/nonregular secret file: $file"
    perms=$(stat -c '%a' "$HQ_SECRETS_DIR/$file")
    [[ $perms == 600 || $perms == 400 ]] || fail "Secret file mode must be 600 or 400: $file"
  done
}

check_install_ports() {
  local port spec path hex
  for spec in "tcp:$HTTP_PORT" "tcp:$LIVE_PORT" "tcp:$METRICS_PORT" "tcp:$API_PORT" "tcp:$HISTORY_PORT" "udp:$IPFIX_PORT" "udp:$SYSLOG_PORT"; do
    port=${spec#*:}; hex=$(printf '%04X' "$port")
    for path in "/proc/net/${spec%%:*}" "/proc/net/${spec%%:*}6"; do
      if [[ -r $path ]] && awk -v port="$hex" 'NR > 1 { n=split($2,a,":"); if(toupper(a[n])==port) found=1 } END {exit !found}' "$path"; then
        fail "Required port is already in use: $spec"
      fi
    done
  done
}

preflight() {
  command -v docker >/dev/null || fail 'Existing Docker engine required.'
  docker info >/dev/null 2>&1 || fail 'Docker engine unavailable.'
  [[ -f /etc/unraid-version || ${HQ_ALLOW_NON_UNRAID:-0} == 1 ]] || fail 'This installer targets Unraid.'
  check_secrets
  local image
  for image in "$HQ_IMAGE_PREFIX-api:$HQ_VERSION" "$HQ_IMAGE_PREFIX-collector:$HQ_VERSION" "$HQ_IMAGE_PREFIX-web:$HQ_VERSION" victoriametrics/victoria-logs:v1.53.0 alpine:3.24; do
    docker image inspect "$image" >/dev/null 2>&1 || fail "Image not available locally; build/pull first: $image"
  done
}

template_preflight() {
  local name src dst
  for name in history collector api web geoip; do
    src="$ROOT/unraid/unifi-hq-$name.xml"
    dst="$HQ_TEMPLATE_DIR/my-unifi-hq-$name.xml"
    [[ -f $src ]] || fail "Missing packaged template: $src"
    [[ ! -L $dst ]] || fail "Refusing symlinked template: $dst"
    if [[ -e $dst ]] && ! cmp -s "$src" "$dst"; then
      [[ $ACTION == update ]] || fail "Template conflict; review before install: $dst"
      grep -Fq "<Name>unifi-hq-$name</Name>" "$dst" || fail "Existing template is not ours: $dst"
    fi
  done
}

install_templates() {
  local name src dst backup="$HQ_DATA_DIR/backups/templates-$(date -u +%Y%m%dT%H%M%SZ)"
  run mkdir -p "$HQ_TEMPLATE_DIR"
  for name in history collector api web geoip; do
    src="$ROOT/unraid/unifi-hq-$name.xml"
    dst="$HQ_TEMPLATE_DIR/my-unifi-hq-$name.xml"
    if [[ -e $dst ]] && ! cmp -s "$src" "$dst"; then
      run mkdir -p "$backup"
      run cp -p "$dst" "$backup/"
    fi
    if [[ ! -e $dst ]] || ! cmp -s "$src" "$dst"; then run cp "$src" "$dst"; fi
  done
}

initialise() {
  run mkdir -p "$HQ_DATA_DIR/config" "$HQ_DATA_DIR/geoip" "$HQ_DATA_DIR/history" "$HQ_DATA_DIR/state"
  # Collector runs uid65534; only the config/GeoIP directories need traversal/read.
  run chmod 755 "$HQ_DATA_DIR" "$HQ_DATA_DIR/config" "$HQ_DATA_DIR/geoip"
  if [[ ! -e $HQ_DATA_DIR/config/hosts.csv ]]; then
    if [[ $APPLY == --apply ]]; then printf 'ip,name\n' > "$HQ_DATA_DIR/config/hosts.csv"; chmod 644 "$HQ_DATA_DIR/config/hosts.csv";
    else printf 'Create %q with CSV header ip,name (only if absent).\n' "$HQ_DATA_DIR/config/hosts.csv"; fi
  fi
}

geoip_job() {
  run docker run --rm --name unifi-hq-geoip --network host --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --memory 128m \
    --label io.unifi-hq.stack=unifi-hq \
    -e "GEOIP_MAX_AGE_DAYS=${GEOIP_MAX_AGE_DAYS:-32}" \
    -v "$ROOT/scripts/geoip-update.sh:/update.sh:ro" -v "$HQ_DATA_DIR/geoip:/geoip" \
    alpine:3.24 /bin/sh /update.sh
}

start_services() {
  run docker run -d --name unifi-hq-history "${common[@]}" --memory 512m \
    -v "$HQ_DATA_DIR/history:/vlogs" victoriametrics/victoria-logs:v1.53.0 \
    "-httpListenAddr=127.0.0.1:$HISTORY_PORT" -storageDataPath=/vlogs \
    "-retentionPeriod=${RETENTION:-30d}" "-retention.maxDiskSpaceUsageBytes=${MAX_DISK:-20GiB}" \
    -inmemoryDataFlushInterval=30s -loggerLevel=WARN -search.maxQueryDuration=60s
  run docker run -d --name unifi-hq-collector "${common[@]}" --memory 384m \
    --tmpfs /var/lib/vector:uid=65534,gid=65534,size=16m \
    -v "$HQ_DATA_DIR/config:/etc/vector/config:ro" -v "$HQ_DATA_DIR/geoip:/geoip:ro" \
    -e "TZ=$TZ" -e "VECTOR_LOG=${VECTOR_LOG:-warn}" -e "VECTOR_THREADS=${VECTOR_THREADS:-1}" \
    -e "WAN_IP=${WAN_IP:-}" -e "ALLOWED_SENDERS=${ALLOWED_SENDERS:-127.0.0.1}" \
    -e "LOCAL_EXTRA_NETS=${LOCAL_EXTRA_NETS:-}" -e "LOCAL_SERVER_PORTS=${LOCAL_SERVER_PORTS:-51820}" \
    -e "IGNORE_IPS=${IGNORE_IPS:-}" -e "IPFIX_PORT=$IPFIX_PORT" -e "SYSLOG_PORT=$SYSLOG_PORT" \
    -e "LIVE_PORT=$LIVE_PORT" -e "METRICS_PORT=$METRICS_PORT" -e "HISTORY_PORT=$HISTORY_PORT" \
    "$HQ_IMAGE_PREFIX-collector:$HQ_VERSION"
  run docker run -d --name unifi-hq-api "${common[@]}" --memory 128m --user 0:0 \
    --cap-add KILL --pid=container:unifi-hq-collector \
    -v "$HQ_DATA_DIR/config:/config" -v "$HQ_DATA_DIR/state:/state" \
    -v "$HQ_SECRETS_DIR:/run/secrets:ro" -e "TZ=$TZ" -e HOSTS_FILE=/config/hosts.csv \
    -e "LISTEN=127.0.0.1:$API_PORT" -e AUTH_PASSWORD_HASH_FILE=/run/secrets/auth_password_hash \
    -e AUTH_SECRET_FILE=/run/secrets/auth_secret -e "UNIFI_API_KEY_FILE=$UNIFI_KEY_FILE" \
    -e "UNIFI_URL=${UNIFI_URL:-}" -e "UNIFI_SITE_ID=${UNIFI_SITE_ID:-}" \
    -e "UNIFI_API_BASE_PATH=${UNIFI_API_BASE_PATH:-/proxy/network/integration/v1}" \
    -e "UNIFI_CA_FILE=${UNIFI_CA_FILE:-}" -e "UNIFI_POLL_SECONDS=${UNIFI_POLL_SECONDS:-15}" \
    -e "UNIFI_STALE_SECONDS=${UNIFI_STALE_SECONDS:-60}" -e INVENTORY_FILE=/state/inventory.json \
    "$HQ_IMAGE_PREFIX-api:$HQ_VERSION"
  run docker run -d --name unifi-hq-web "${common[@]}" --memory 64m \
    --tmpfs /data:size=1m --tmpfs /config:size=1m \
    --label "net.unraid.docker.webui=http://[IP]:$HTTP_PORT/" \
    --label "net.unraid.docker.icon=https://raw.githubusercontent.com/MephistoJB/unifi-HQ/main/web/favicon.svg" \
    -e "HTTP_PORT=$HTTP_PORT" -e "API_PORT=$API_PORT" -e "LIVE_PORT=$LIVE_PORT" -e "HISTORY_PORT=$HISTORY_PORT" \
    -e "ALLOWED_CLIENTS=${ALLOWED_CLIENTS:-private_ranges}" -e "HOME_LAT=${HOME_LAT:-50.11}" \
    -e "HOME_LON=${HOME_LON:-8.68}" -e "HOME_LABEL=${HOME_LABEL:-Home}" -e "GATEWAY_NAME=${GATEWAY_NAME:-Gateway}" \
    "$HQ_IMAGE_PREFIX-web:$HQ_VERSION"
}

owned() { [[ $(docker inspect --format '{{index .Config.Labels "io.unifi-hq.stack"}}' "$1") == unifi-hq ]]; }
stop_services() {
  local name
  # Check the entire set before stopping any service.
  for name in unifi-hq-web unifi-hq-api unifi-hq-collector unifi-hq-history; do
    if docker container inspect "$name" >/dev/null 2>&1; then
      owned "$name" || fail "Container $name is not owned by this stack."
    fi
  done
  for name in unifi-hq-web unifi-hq-api unifi-hq-collector unifi-hq-history; do
    if docker container inspect "$name" >/dev/null 2>&1; then
      run docker stop "$name"
      [[ $ACTION != update ]] || run docker rm "$name"
    fi
  done
}

validate_config
case "$ACTION" in
  preview) start_services ;;
  install)
    if [[ $APPLY == --apply ]]; then
      preflight
      template_preflight
      check_install_ports
      for name in unifi-hq-history unifi-hq-collector unifi-hq-api unifi-hq-web; do
        if docker container inspect "$name" >/dev/null 2>&1; then fail "Container $name exists; use update after a backup."; fi
      done
    fi
    initialise; geoip_job; start_services; install_templates ;;
  update)
    if [[ $APPLY == --apply ]]; then preflight; template_preflight; fi
    stop_services; initialise; geoip_job; start_services; install_templates ;;
  templates)
    [[ $APPLY != --apply ]] || template_preflight
    install_templates ;;
  stop) stop_services ;;
  geoip) geoip_job ;;
  *) fail 'Usage: unraid.sh preview|install|update|stop|geoip|templates [--apply]' ;;
esac

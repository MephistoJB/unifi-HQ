#!/usr/bin/env bash
# Local end-to-end pipeline test. Sends only loopback test datagrams, no traffic
# to the addresses contained in their payloads. Synthetic records remain labelled
# by their test-only source/rule in history. No Docker Compose/plugin required.
set -euo pipefail
IPFIX_PORT=${IPFIX_PORT:-2055}
SYSLOG_PORT=${SYSLOG_PORT:-5514}
HISTORY_PORT=${HISTORY_PORT:-9428}
for n in "$IPFIX_PORT" "$SYSLOG_PORT" "$HISTORY_PORT"; do
  [[ $n =~ ^[0-9]+$ ]] && ((n>1024 && n<=65535)) || exit 2
done
marker=$(date +%s)
hex="000a0055$(printf '%08x' "$marker")0000000000001092"
hex+="000200240100000700080004000c000400070002000b0002000400010001000800020008"
hex+="010000210a250afa090909099c4001bb0600000000000010920000000000000007"
escaped=$(printf '%s' "$hex" | sed 's/../\\x&/g')
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT
printf '%b' "$escaped" > "$payload"
# cat emits this 85-byte fixture in one write/datagram; printf may split writes.
cat "$payload" > "/dev/udp/127.0.0.1/$IPFIX_PORT"
printf '<4>%s HQ-Selbsttest kernel: [WAN_LOCAL-B-99999] DESCR="UniFi HQ Selbsttest %s" IN=eth4 OUT= SRC=45.33.32.156 DST=203.0.113.1 PROTO=TCP SPT=40000 DPT=22' \
  "$(LC_ALL=C date '+%b %e %H:%M:%S')" "$marker" > "/dev/udp/127.0.0.1/$SYSLOG_PORT"
query(){ curl -fsSG --max-time 5 "http://127.0.0.1:$HISTORY_PORT/select/logsql/query" --data-urlencode "query=$1"; }
flow=;blocked=
for ((attempt=0;attempt<20;attempt++)); do
  flow=$(query '_time:2m local_ip:"10.37.10.250" remote_ip:"9.9.9.9" | stats count() n' || true)
  blocked=$(query "_time:2m dir:blocked rule:\"Selbsttest $marker\" | stats count() n" || true)
  if [[ $flow =~ \"n\":\"[1-9][0-9]*\" && $blocked =~ \"n\":\"[1-9][0-9]*\" ]]; then
    printf 'PASS IPFIX -> GoFlow2 -> Vector -> VictoriaLogs\nPASS Syslog -> Vector -> VictoriaLogs\n'
    printf 'Synthetic self-test marker: %s (not real network traffic).\n' "$marker"
    exit 0
  fi
  sleep 1
done
printf 'FAIL pipeline self-test. Inspect only UniFi HQ collector/history logs.\n' >&2
exit 1

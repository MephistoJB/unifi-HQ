# Official UniFi inventory API extension

The extension polls only documented **GET** endpoints of the local UniFi Network integration API. It does not require Home Assistant, the UniFi MCP server, or a cloud connector. The first version is based on the official Network 10.6.106 OpenAPI schema and requires live verification against the target controller before being described as fully commissioned.

## Runtime configuration

| Variable | Default | Meaning |
|---|---|---|
| `UNIFI_URL` | empty | HTTPS origin of the local console; no username/password/query in this URL |
| `UNIFI_API_BASE_PATH` | `/proxy/network/integration/v1` | Documented integration API prefix |
| `UNIFI_SITE_ID` | empty | Official site UUID; an empty value discovers a site only if exactly one site exists |
| `UNIFI_API_KEY_FILE` | empty | Read-only mounted secret file containing the API key |
| `UNIFI_API_KEY` | empty | Alternative in-memory environment injection; file takes precedence |
| `UNIFI_CA_FILE` | empty | Optional custom CA PEM bundle; system trust is used otherwise; TLS verification is always enabled |
| `UNIFI_POLL_SECONDS` | `15` | Delay between completed collections, 10–300 seconds |
| `UNIFI_STALE_SECONDS` | `60` | Snapshot freshness limit, 30–900 seconds and at least two polling delays |
| `INVENTORY_FILE` | `/state/inventory.json` | Atomic snapshot, written with mode 0600 |

The existing authentication additionally supports `AUTH_PASSWORD_HASH_FILE` and `AUTH_SECRET_FILE`. An explicitly configured but unreadable secret file fails closed; it never silently disables login.

### Approved WAN telemetry extension

`GET /api/wan` uses the same application authentication. A separate in-process poller reuses the existing TLS-verified local origin and API key to read only `/proxy/network/api/s/{legacySite}/stat/device/{gatewayMAC}`. `UNIFI_LEGACY_SITE` defaults to `default` and identifies the classic site shortname, independently of the official `UNIFI_SITE_ID` UUID. The gateway MAC is discovered from the inventory, never hardcoded.

The response contains `configured`, `status`, `stale`, nullable `public_ip`, `upload_bps`, `download_bps`, `sampled_at`, `last_success_at`, `last_attempt_at`, `active_interface`, `source`, `poll_interval_seconds` and `stale_after_seconds`. `source` is `unifi_local_classic`; states are `ok`, `partial`, `loading`, `unconfigured`, `unavailable`, `error` or `stale`.

Public IP comes exclusively from a uniquely identified live `active_geo_info` entry, excluding addresses explicitly marked inactive in `last_wan_interfaces`. The private `wan1.ip` is not a public-IP fallback. Rates come from the uniquely active (`up` and `is_uplink`) WAN's `tx_bytes-r` and `rx_bytes-r`, converted from verified bytes/second to upload/download bits/second. Missing/null/invalid fields remain unavailable; actual numeric zero represents measured idle traffic. More than one active WAN or more than one gateway is reported as unavailable instead of silently aggregating or choosing one.

Measurement freshness uses the gateway's `last_seen` timestamp. An HTTP success does not make an old sample current. Last-known values may remain in the JSON with `stale: true`; the frontend must hide them or explicitly mark them stale. WAN failures never change inventory freshness or device status. There are no new credentials, external IP-lookup calls, services, ports or runtime dependencies. WAN metrics are memory-only and not restored as live values after a restart.

Controller responses, credentials and private URLs are not included in logs or frontend errors. HTTP redirects are not followed, so a redirect cannot forward an API key to another origin. The API image includes the standard CA certificate bundle.

## Frontend endpoint

`GET /api/inventory` is protected by the existing application login. It returns HTTP 200 for inventory setup, loading and error states so the frontend can present actionable state rather than substitute an empty list.

Top-level fields: `configured`, `generated_at`, `last_success_at`, `stale`, `error`, `devices`, `counts`, `sources`, `poll_interval_seconds`, `stale_after_seconds`.

Each device has `id`, `name`, `kind`, `status`, `last_status`, `connection_type`, `addresses`, `historical_attachment`; available details include `mac`, `ip`, `parent_id`, `port`, `link_speed_mbps`, `last_seen`, `model` and `network`. Empty optional properties are omitted. Device kind is `gateway`, `switch`, `ap`, `client`, or generic `device` when classification is unavailable. All names are untrusted data and must be inserted into HTML as text.

`sources.controller.status` is `unconfigured`, `loading`, `ok`, `error` or `stale`, with attempt/success timestamps and a sanitized error. `sources.topology` carries explicit coverage limitations. Times are UTC RFC 3339 strings.

## State and completeness rules

- Both list endpoints must be complete and consistent across all pages. Duplicate page IDs, changing total counts, mismatched counts, invalid details and request failures abort the collection.
- Infrastructure uses official `state`; connected-client list membership establishes current online state. Each listed infrastructure device is enriched with its documented details to obtain the parent ID.
- Local clients use site plus MAC identity; clients without MAC use the official stable client ID. Names and IP addresses never merge identities.
- Missing devices become provisional `unknown` after one successful complete absence and `offline` after two consecutive successful complete absences. A controller error resets the missing streak.
- An API failure immediately renders cached states `unknown`; stale age also does so. `last_status`, `last_seen` and the last attachment remain available.
- A snapshot loaded after API restart remains unknown until the first fresh successful collection. A recent file alone is not proof of a current connection.
- Historical attachment is explicitly marked. An unobserved parent is not inferred from an address, subnet, a device name or a traffic flow.

## Coverage limits

The documented API provides device parent IDs and wired/wireless client attachment IDs. The pinned schema does not provide reliable client port numbers, VLAN/network mappings or uplink link rates. Those fields stay absent unless actually returned as explicit data; the UI must show them as unknown. Non-UniFi intermediate devices may remain invisible.

Disconnected clients are retained only after this application has observed them. No legacy controller-history endpoint is used and no pre-installation uptime is invented. The extension currently persists the latest inventory and attachments, not a historical uptime percentage. Existing traffic history remains independently available in VictoriaLogs.

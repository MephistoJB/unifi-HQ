# UniFi HQ 0.1.0 verification

Verified 10 November 2026 against upstream commit `d4b54fdde16a625c1816801600803e3be03b244c`.

## Checked behavior

- Go API tests and `go vet` pass, including the existing login tests and nine inventory test functions with table subtests.
- Four frontend model tests pass: stale status, ancestry when filtering, cycle handling and combined filters.
- Chromium layout checks at 810 × 1080, 1080 × 810 and 1440 × 1000 show no horizontal page overflow or JavaScript exceptions in the inventory views.
- Touch interaction opens and closes device details; tree expansion, search, status filtering and diagram zoom work. Expanders and primary controls have at least 44 × 44 CSS pixel touch areas.
- When WebGL is unavailable, the existing flat map starts successfully and the rest of the application remains available.
- The actual Unraid installation passes login and authenticated inventory checks. Unauthenticated inventory requests return HTTP 401.
- All four release containers run on an x86_64 Unraid target. Caddy and the Vector transform configuration validate.
- Loopback IPFIX and syslog fixtures reach VictoriaLogs through the real collector pipeline. These are synthetic records, not captured gateway traffic. `scripts/smoke-unraid.sh` can repeat this check without Docker Compose.

## Screenshots and reports

The `ipad9-*` and `desktop-*` screenshots use explicit fictional demo data. The `installed-demo-*` screenshots show that same demo through the deployed, authenticated application. `installed-unconfigured-ipad9.png` shows the real initial setup state. JSON files contain browser check results; no login tokens or production credentials are included.

These viewport checks are Chromium device-size emulation. They are not a test on physical iPad hardware or the Safari engine.

## Remaining live integration checks

The deployment currently has no UniFi controller API key or URL configured. The interface reports this explicitly and does not show fictional live devices. The official controller response, certificate trust, actual topology completeness, device-disconnect behavior and gateway IPFIX export must be verified after the connection is configured.

The official API schema supports parent device relationships but does not guarantee per-client ports, VLANs or uplink link speeds. Missing fields remain unavailable. Offline client retention starts with the application's own observations; there is no fabricated historical availability timeline. Traffic dossiers are IP-scoped and explain the identity limit when addresses change.

The source changes and versioned container images form an independent fork. The upstream Proxmox installation scripts remain reference material; use the native Unraid guide in `../unraid.md` for this fork.

## Follow-up verification on 11 November 2026

The existing UniFi MCP controller login was used to configure a dedicated official API key. The local API connection now verifies TLS normally. Real controller inventory, parent relationships and natural client transitions from unknown to offline were observed through the deployed UI. Gateway IPFIX v10 export and syslog forwarding were enabled through the supported UniFi interface after a fresh controller backup; real outbound/internal flow records and blocked events reached VictoriaLogs. Live screenshots contain private network information and are retained outside the public repository.

Release `0.1.1` fixes a live-data classification issue: newer Network versions return gateway model names such as `UniFi Dream Machine PRO SE` while advertising integrated switching capabilities. Known gateway models now take priority over integrated switch/AP capabilities; regression tests include the real response shape and unknown-hardware controls. The earlier unconfigured screenshots and test report above document the initial 10 November deployment.

## Topology graph release 0.2.0

The former “Netzwerkbaum” view is now named “Topologie”. A locally vendored vis-network 10.1.5 standalone build replaces the custom SVG graph layout and pointer handling. Its pinned package provenance, integrity and MIT license are kept in `web/vendor/`. No CDN, frontend framework, additional container or backend API is required.

The graph is the default at all screen sizes, with device silhouettes, online/offline/unknown indicators, optional grouped clients, collapsible branches, vertical/horizontal direction, touch pan/pinch, search ancestry and details. Updates preserve the viewport; direct links reveal grouped clients. Missing attachment types and historical links remain distinguishable. Connection metadata appears in tooltips to avoid overlapping graph labels. The DOM hierarchy remains available for keyboard and assistive access.

Verification: eight model tests and 29 Chromium browser assertions passed. Browser coverage includes 810 × 1080, 1080 × 810 and 1440 × 1000 without horizontal overflow or JavaScript exceptions, touch selection/pan/pinch, group expansion, filters, viewport preservation, deep links, failed refresh, freshness expiry and recovery. Physics is disabled. The fictional demo screenshots and test report are in `topology-v020/`; private installed-network screenshots are retained separately. A physical iPad/Safari test has not been performed.

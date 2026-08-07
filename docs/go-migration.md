# Go migration plan

This document defines the compatibility and safety boundary for migrating
`ups-pi-node` from Python to Go. The working Python package remains the
rollback path until every phase has passed tests on the Raspberry Pi Zero 2 W.

## Why the migration is phased

The measured startup delay is dominated by NetworkManager and hotspot setup,
not by Flask process startup alone. Go provides a small, statically compiled
runtime and simpler deployment, but the migration must first remove the Wi-Fi
state race. Hardware control and the browser application move only after the
new privileged boundary is verified.

## Target architecture

```text
Browser (TypeScript, SVG/CSS animations)
          | REST + SSE
          v
Unprivileged Go HTTP service
          | versioned JSON over Unix stream socket
          v
Root-owned Go helper
          | NetworkManager D-Bus, I2C, GPIO, SPI
          v
Wi-Fi / INA3221 / relays / ST7735
```

The web service never receives direct access to NetworkManager, GPIO, I2C, or
SPI. The privileged helper is installed in a root-owned, non-writable location
and exposes only allow-listed operations through its Unix socket.

## Helper protocol v1

The first Go helper must be wire-compatible with the current Python client.
The transport is an `AF_UNIX` stream socket. A client writes one UTF-8 JSON
object, closes its write side, and reads one JSON response until EOF. Requests
larger than 1 MiB are rejected.

Request:

```json
{"action":"wifi.status","params":{"interface":"wlan0","hotspot_connection_name":"ups-pi-node-hotspot"}}
```

Successful response:

```json
{"success":true,"data":{}}
```

Failed response:

```json
{"success":false,"error":"human-readable message"}
```

The v1 allow-list and response compatibility requirements are:

| Action | Required parameters | Compatible result |
| --- | --- | --- |
| `auth.pam` | `username`, `password`; optional `service` | `authenticated` boolean |
| `wifi.status` | `interface`, `hotspot_connection_name`; optional `portal_mode` | portal mode, SSID/connection, IPv4 address, state |
| `wifi.scan` | `interface` | sorted `networks` with SSID, signal, security, connected |
| `wifi.connect` | `interface`, `ssid`; optional password, hidden, and `ipv6` (`disabled` default, or `auto`) | completion `message` |
| `ups.status` | none | existing battery, output, AC, relay, and backend fields |

An incompatible protocol will use a separate socket or an explicit versioned
action. Existing v1 fields may be extended but not renamed or removed during
the migration.

## Wi-Fi state machine

NetworkManager is the single source of truth. The Go implementation uses its
system D-Bus API instead of parsing `nmcli` output. Operations are serialized
per Wi-Fi device and are idempotent.

| Observed state | Fallback action |
| --- | --- |
| Client connected | No-op |
| Hotspot connected | No-op |
| Connecting or activating | No-op; allow transition to finish |
| Disconnected within grace interval | Try known autoconnect client profiles |
| Still disconnected after grace/retries | Create or update one managed hotspot profile, then activate it once |
| Device unavailable/disabled | Report state; do not force activation |

For a requested SSID, the helper updates or reuses one canonical managed
profile. Re-entering a password must not create duplicate profiles. Same-SSID
duplicate profiles created by earlier helpers may be deleted after the
canonical profile exists; unrelated profiles are left alone. A manual connect
operation suppresses fallback activation until that transition reaches a
terminal state or times out.

Client Wi-Fi profiles disable IPv6 by default (`ipv6.method=ignore` in
NetworkManager). The Go web service reads `[wifi] ipv6 = disabled|auto` or
`UPS_PI_NODE_WIFI_IPV6`; existing profiles are normalized on connect so legacy
`ipv6.addresses` arrays are not sent back through D-Bus.

## Deployment and ownership

- Go binaries and static assets: root-owned and not writable by the web user.
- Helper socket: root-owned, group-readable/writable only by the web service
  group, mode `0660`.
- Configuration containing secrets: root-owned and readable only by the
  minimum required service group.
- The Go helper initially runs on a shadow socket while the Python helper owns
  `/run/ups-pi-node/helper.sock`.
- The shadow service is `ups-pi-node-helper-go.service` and listens on
  `/run/ups-pi-node/go-helper.sock`; enabling it is an explicit validation step,
  not an automatic package cutover.
- systemd hardening is added per service after its required device and D-Bus
  access is measured; capabilities and writable paths remain allow-listed.
- Secrets are never passed in command-line arguments or written to logs.

## Phases and acceptance criteria

1. **Fallback stabilization.** Repeated timer runs do not reactivate an active
   hotspot, touch an active client, or interrupt a connection attempt.
2. **Go protocol and NetworkManager layer.** Protocol unit tests cover malformed
   and oversized requests. A fake D-Bus backend proves all state transitions,
   profile reuse, and retry/grace behavior.
3. **Shadow helper.** On-device comparison shows compatible Wi-Fi status/scan
   results. Connect and fallback tests recover to the hotspot without manual
   repair.
4. **Hardware migration.** INA3221 channel 1 and channel 2 values, current
   polarity, relay decisions, and the 128x160 portrait ST7735 output match the
   Python implementation in mock tests and on hardware.
5. **Go web service and dashboard.** REST mutations require authentication and
   CSRF protection; SSE reconnects cleanly and is rate-limited. The dashboard
   fits the 128x160 device display where relevant and common portrait mobile
   browsers, respects reduced-motion preferences, and remains usable without
   animation.
6. **Cutover.** Debian upgrade preserves config and data, services survive
   reboot and network loss, security checks pass, and the Python package can be
   restored until the Go release is field-proven.

## Remaining full-Go cutover checklist

The Go runtime is not complete until every Python user-facing function below is
available through the Go service or intentionally removed from both runtimes:

1. **External integrations.** Preserve `/api/integrations/metrics`,
   `/api/integrations/zabbix`, `/api/integrations/zabbix/discovery`,
   `/api/integrations/zabbix/<metric>`, and
   `/api/integrations/home-assistant`, including the optional
   `X-UPS-PI-NODE-Token` / `token` access gate.
2. **System settings.** Moved load timeout, UI language, hotspot SSID/password,
   and persisted INI writes to authenticated Go handlers.
3. **Dashboard widgets.** Preserved the built-in widget list, active widget
   layout, add/remove actions, and safe persistence under
   `/var/lib/ups-pi-node`.
4. **Custom widget packages.** Preserved ZIP upload, manifest validation,
   CSS/asset/font serving, size limits, path traversal checks, delete, and
   ownership expectations.
5. **UI parity and copy hygiene.** Keep labels short and technical. The Go UI
   must not add decorative or explanatory captions that consume screen space
   without carrying state, action, or measurement data. Banned examples include
   `LIVE POWER MAP`, `POWER TELEMETRY`, `Живлення під контролем`,
   `Мережа активна: UPS живить вихід`, `Керування живленням, виходом 19 В і
   мережею ноди`, `where power flows`, `how current flows`, `як йде струм`, and
   similar copy. Tests should block these strings from returning.
6. **Runtime default.** Make `go` the default only after the items above pass
   local tests, package install/upgrade tests, reboot tests, Wi-Fi loss tests,
   and live hardware checks on the Raspberry Pi Zero 2 W.
7. **Python removal.** Remove Python services, virtualenv setup, Flask assets,
   and Python dependencies only after one field-proven Go release can still be
   rolled back from a package repository.

## Non-goals for the first phase

- Replacing all Python code in one release.
- Deleting unrelated NetworkManager profiles.
- Moving animation rendering into the Raspberry Pi backend.
- Changing the verified INA3221, GPIO, or ST7735 wiring.

## 2026-08-07 web UI parity checkpoint

- The Go web service restores Flask-era `/wifi` and `/system` pages and the
  UPS/Wi-Fi/System navigation tabs.
- Authenticated Go handlers now persist UI language, hotspot SSID/password,
  and load timeout settings back to the shared INI file.
- Go preserves dashboard widget add/remove state in the existing
  `dashboard-widgets.json` format.
- Go restores custom UPS widget ZIP install/delete and static serving for
  widget CSS/assets from the existing widgets directory.
- `/api/data` is available again for Flask-compatible live widget updates.
- The Go web unit runs as `www-data` with explicit write access to
  `/etc/ups-pi-node` and `/var/lib/ups-pi-node`, matching the restored write
  requirements while keeping hardware access behind the helper socket.

## 2026-07-18 implementation checkpoint

- The Bash fallback is idempotent for `connected` and `connecting`. On the live
  node two manual reconciliations and a real 30-second timer tick preserved the
  same `/org/freedesktop/NetworkManager/ActiveConnection/301` hotspot object.
- The node then joined `MyRouter` through the canonical managed profile
  `ups-pi-node-MyRouter-b3a70bc4` without creating another portal profile. DHCP
  changed the node address from `192.0.2.10` to `192.0.2.40`.
- The root-owned shadow helper is running separately on
  `/run/ups-pi-node/go-helper.sock`. Its `wifi.status` response matched the
  Python helper field-for-field, and `wifi.scan` returned the compatible
  network schema while the working helper remained active.
- The Go INA3221 register math is covered for channels 1, 2, and 3, both current
  directions, two identical `0.00375 ohm` shunts, and configuration `0x6527`.
  Live diagnostics attach without rewriting CONFIG. Linux SMBus ioctl buffers
  are pinned for armhf; Python and Go both observe the same fast channel-2 load
  variation rather than treating a single snapshot as a stable current.
- Relay policy and ordering are tested without claiming live GPIO lines. The Go
  ST7735 renderer produces a 128x160 Ubuntu-font portrait framebuffer and
  converts it to the same physical 160x128, RGB666, `MADCTL=0x60` layout used by
  the verified luma.lcd driver. GPIO and SPI cutover remain disabled while the
  Python hardware controller owns those devices.

## 2026-07-19 hardware and deployment checkpoint

- A controlled hand-off stopped the Python helper before starting the Go
  hardware owner. The Go controller claimed GPIO17/18/22/24/25/27, I2C1, and
  SPI0 without `hardware_error` or `display_error`; the Python helper was
  restored after each test.
- Live `ups.status` samples reported approximately `12.14-12.28 V` on battery
  channel 1 and `18.96-19.10 V` on output channel 2, with AC and relay state
  preserved. Channel-2 current varied with the real load as expected.
- The first Go ST7735 test exposed a 180-degree orientation error. The portrait
  mapping was reversed, covered by all four framebuffer corners, redeployed,
  and visually confirmed upright on the physical display.
- The Go web service passed live PAM success/failure, authenticated dashboard,
  REST, CSRF, Wi-Fi scan, SSE sequencing/reconnect, security-header, and Unix
  socket ownership checks while nginx still served Python.
- Debian `0.3.0` packages both runtimes. `/usr/sbin/ups-pi-node-runtime` performs
  a health-checked hardware hand-off and atomically changes the nginx upstream;
  a failed Go transition restores Python. The selected runtime is recorded in
  `/etc/ups-pi-node/runtime` and survives upgrades.

Runtime commands:

```bash
sudo ups-pi-node-runtime status
sudo ups-pi-node-runtime apply go
sudo ups-pi-node-runtime apply python
```

The package defaults to `python` on the first `0.3.0` upgrade. Go becomes the
boot runtime only after the explicit, successful `apply go` transaction.

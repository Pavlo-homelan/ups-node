# Integrations

`ups-pi-node` exposes integration metrics separately from the web UI. These
payloads intentionally do not include Wi-Fi SSID, Wi-Fi mode, hotspot state, or
portal mode. Network state is only used internally to decide whether external
publishing is possible.

## Useful Metrics

Endpoint:

```text
GET /api/integrations/metrics
GET /api/integrations/zabbix
GET /api/integrations/zabbix/<metric-key>
GET /api/integrations/zabbix/discovery
GET /api/integrations/home-assistant
```

Current metric keys:

```text
app.health
ups.ac_present
ups.battery.percent
ups.battery.status_code
ups.battery.charging
ups.battery.discharging
ups.voltage.v
ups.current.ma
ups.current.abs_ma
ups.power.w
ups.output.voltage_v
ups.output.current_ma
ups.output.power_w
system.cpu.temp_c
system.ram.used_mb
system.ram.percent
```

The `ups.voltage.v`, `ups.current.ma`, and `ups.power.w` values describe the
battery path on INA3221 channel 1. The `ups.output.*` values describe the 19 V
path on channel 2. Both channels use identical FL-2C `20 A / 75 mV` shunts on
the verified node. Positive output current means current flows from the 19 V
source to the load.

See [hardware-validation-2026-07-18.md](hardware-validation-2026-07-18.md) for
the validated channel map and example live readings.

## Access Token

By default the metrics endpoints are plain HTTP endpoints. If a token is set,
requests must send it in `X-UPS-PI-NODE-Token` or as `?token=...`.

```ini
[integrations]
node_id = ups-pi-node
token =
```

Environment override:

```text
UPS_PI_NODE_NODE_ID=ups-pi-node
UPS_PI_NODE_INTEGRATIONS_TOKEN=
```

## Zabbix

For a local Zabbix agent, install:

```text
deploy/zabbix/ups-pi-node-agent.conf
```

as:

```text
/etc/zabbix/zabbix_agent2.d/ups-pi-node.conf
```

Example checks:

```bash
zabbix_agent2 -t ups-pi-node.metric[ups.battery.percent]
zabbix_agent2 -t ups-pi-node.metric[ups.ac_present]
zabbix_agent2 -t ups-pi-node.metrics
```

The Zabbix host becoming unavailable is the correct signal for network loss.
Wi-Fi mode is not exported as a metric.

## Home Assistant

Home Assistant should consume only UPS/system entities. The discovery payload is
available at:

```text
GET /api/integrations/home-assistant
```

It returns MQTT discovery topics and payloads for the useful metrics above.
When MQTT publishing is added, publishing should be skipped while the node is in
fallback AP/setup mode; no Wi-Fi entities should be created in Home Assistant.

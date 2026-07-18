# Hardware Validation — 2026-07-18

This document records the hardware configuration and live observations from the
Raspberry Pi Zero 2 W UPS node validated on 2026-07-18. Values below are point-in-time
operational readings, not calibration certificates.

## Validated Configuration

| Component | Verified configuration |
| --- | --- |
| Raspberry Pi | Raspberry Pi Zero 2 W |
| Power monitor | INA3221, I2C bus 1, address `0x40` |
| Battery telemetry | INA3221 channel 1 |
| 19 V output telemetry | INA3221 channel 2 |
| Shunts | Two identical FL-2C `20 A / 75 mV` shunts (`0.00375 ohm`) |
| Display | ST7735, effective `128x160` portrait orientation |
| Display fonts | Ubuntu and Ubuntu Mono from `fonts-ubuntu` |
| Installed package | `ups-pi-node 0.2.2-6` |
| Network mode | Wi-Fi client with fallback hotspot available |

INA3221 channel 3 is currently unused.

## Power Topology

The Raspberry Pi is powered independently from the measured 19 V output:

```text
DC power supply -> DC buck converter -> dedicated UPS -> Raspberry Pi
```

Power is soldered to the marked pads on the underside of the Raspberry Pi USB
power connector. Verify the pad labels and polarity with a multimeter before
connecting the Pi. Do not infer pad polarity from cable color or connector
position.

The INA3221 `VS` pin is connected to Raspberry Pi physical pin 1 (`3.3 V`). A
polarized connector such as XT30 reduces the risk of an accidental reversed
connection. Its polarity must still be verified when the connector pair is
assembled for the first time.

## Raspberry Pi Power Header Reference

These are physical pin numbers on the 40-pin header:

| Rail | Physical pins | Notes |
| --- | --- | --- |
| `3.3 V` | 1, 17 | INA3221 `VS` is connected to pin 1 |
| `5 V` | 2, 4 | Direct 5 V rail; do not confuse with pin 1 |
| `GND` | 6, 9, 14, 20, 25, 30, 34, 39 | All are common ground |

The INA219 IC supports a `3.0-5.5 V` supply and the INA3221 supports
`2.7-5.5 V`, so either IC can operate from 5 V. Supply capability alone does
not make arbitrary INA219 and INA3221 modules pin-compatible. Before a
contact-for-contact module replacement, check the module silkscreen or
schematic for `VS/VCC`, `GND`, `SDA`, `SCL`, and any I2C pull-ups. Raspberry Pi
GPIO uses 3.3 V logic.

The supply ranges come from the official Texas Instruments
[INA219](https://www.ti.com/lit/ds/symlink/ina219.pdf) and
[INA3221](https://www.ti.com/lit/ds/symlink/ina3221.pdf) datasheets.

## INA3221 Wiring And Channel Map

Raspberry Pi connection:

| INA3221 pin | Raspberry Pi physical pin | Signal |
| --- | ---: | --- |
| `VS` / `VCC` | 1 | `3.3 V` |
| `GND` | 6 | Ground |
| `SDA` | 3 | BCM GPIO2 / I2C1 SDA |
| `SCL` | 5 | BCM GPIO3 / I2C1 SCL |

Measured paths:

```text
Channel 1: charger/relay side -> FL-2C -> battery positive
Channel 2: 19 V source side   -> FL-2C -> 19 V load side
```

For each channel, `IN+` is on the source side and `IN-` is on the load side.
This gives positive current for battery charging and for current delivered by
the 19 V output. The configured shunt resistance is derived from the marking:

```text
R = 0.075 V / 20 A = 0.00375 ohm
```

The high-current path must pass through the FL-2C terminals. Only Kelvin sense
wires go to the INA3221 inputs. The two installed shunts have the same rating,
so both `shunt_ohms` and `output_shunt_ohms` are `0.00375`.

## Live Readings

Two snapshots were observed while validating channel 2 and the portrait display:

| Snapshot | Battery | 19 V output | State |
| --- | --- | --- | --- |
| Channel 2 bring-up | `12.128 V`, `+213.33 mA` | `18.960 V`, `+394.67 mA`, `7.483 W` | AC present |
| Portrait UI check | `12.160 V`, `+213.33 mA`, `2.594 W` | `18.984 V`, `+1994.67 mA`, `37.867 W` | `CHARGE`, AC present, battery 86% |

The second output-current reading reflects the load present at that moment. It
must not be used as a fixed expected value. Both helper snapshots were returned
without `hardware_error` or `display_error`.

## Display

The physical panel is portrait. The driver configuration retains the controller
dimensions `160x128` with `rotate = 1`; the resulting luma display bounds are
`128x160`. The renderer detects those bounds and uses a stacked layout:

```text
+------------------+
| UPS NODE   AC OK |
+------------------+
| BATTERY          |
| voltage/current  |
| charge and state |
+------------------+
| 19V OUTPUT / CH2 |
| voltage/current  |
| power            |
+------------------+
```

Landscape bounds still select the two-column layout automatically.

## Network And Service Check

At validation time the node was connected as a Wi-Fi client and received
`192.0.2.10` by DHCP. The preferred address is the mDNS hostname:

```text
http://zero2.local/
```

The DHCP address is an observation, not a permanent configuration. The fallback
hotspot remains available at `10.42.0.1` when the node cannot join a known
network.

Post-upgrade checks confirmed:

- `ups-pi-node-helper.service`: active;
- `ups-pi-node.service`: active;
- web root: HTTP 200;
- INA3221 backend: active, channels 1 and 2 returning data;
- display initialization and rendering: no reported error.

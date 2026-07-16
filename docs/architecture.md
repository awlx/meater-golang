# Architecture

## Project layout

| Path                                | Purpose                                                  |
| ------------------------------------ | -------------------------------------------------------- |
| `main.go`                           | Flags, source selection, HTTP(S) server startup.          |
| `ble.go` / `ble_disabled.go`        | Local BLE source: scan/connect/subscribe. Compiled out by `-tags nobluetooth`. |
| `bridge.go`                         | Remote source: reads the probe from a networked ESP32 BLE bridge (`-bridge`). |
| `internal/meater/meater.go`         | BLE UUIDs and the temperature payload decoder.           |
| `internal/monitor/monitor.go`       | Thread-safe state, history, ETA, and SSE fan-out.        |
| `internal/server/server.go`         | HTTP routes, JSON API, and the SSE stream.               |
| `internal/server/web/`              | Static dashboard (HTML/CSS/JS, PWA manifest, worker).    |
| `internal/metrics/metrics.go`       | Prometheus collector behind `/metrics`.                  |
| `custom_components/meater_golang/`  | Home Assistant integration, installable via HACS (Python, not part of the Go module). |
| `hacs.json`                         | Marks the repository as a HACS custom repository.        |
| `bluez_linux.go` / `bluez_other.go` | Platform helpers for BlueZ.                              |
| `firmware/`                         | ESP32 bridge firmware (PlatformIO/C++, not part of the Go module). See [remote-bridge.md](remote-bridge.md). |
| `deploy/meater.service`             | Sample systemd unit.                                     |
| `Dockerfile` / `docker-compose.yml` | Container build and ready-to-run Compose setup.          |

## How the decoding works

The temperature characteristic delivers a little-endian sequence of `uint16`
sensor values. This program targets the 12-byte "resolution 32" payload used by
recent MEATER+ firmware:

- `data[0:2]` — internal (meat tip) sensor.
- `data[10:12]` — ambient (cook) sensor.

Each raw value is converted to Celsius with the probe's fixed scale
`(raw + 8) / 32`. The `/32` scale lets the internal channel span the full
cooking range (a 95 °C target is raw 3032) instead of saturating near 64 °C, and
the ambient sensor at offset 10 is the channel that visibly swings with the cook
temperature. Both were validated against the official app. See
[`internal/meater/meater.go`](../internal/meater/meater.go) for the exact formula
and [`internal/meater/meater_test.go`](../internal/meater/meater_test.go) for
validated sample payloads.

> Note: the MEATER BLE protocol is not officially published. The decoding here
> follows community reverse-engineering of the probe and was checked against the
> official app's readout.

# meater-golang

Read the [MEATER](https://meater.com/) wireless meat thermometer probe over
Bluetooth Low Energy from Go — with a clean, self-hosted web UI.

The program scans for a probe advertising the name `MEATER`, connects to it,
subscribes to the temperature characteristic, decodes the **tip** (internal
meat) and **ambient** (cook) temperatures, and serves a live dashboard with a
temperature chart, doneness targets, ETA, and browser alerts.

![MEATER Monitor web UI](docs/screenshot.png)

## Highlights

- **Everything stays on your network — no MEATER account, no cloud, ever.**
  The probe is read directly over Bluetooth (or the [ESP32
  bridge](docs/remote-bridge.md) below) and served from your own instance;
  the [Home Assistant integration](docs/home-assistant.md) talks straight to
  that instance over the LAN too, so nothing about your cook ever leaves your
  network.
- **Live web dashboard** — a temperature-over-time chart, doneness presets, and
  an ETA that stays sane through a stall, all pushed to the browser in real
  time (no polling), for as many phones/laptops as want to watch.
- **📡 Put the probe anywhere — no Bluetooth range needed at the server.** The
  host running this program normally has to be within a few meters of the
  probe, which is awkward when the grill is outside and the server lives
  indoors. Add a cheap **ESP32 bridge** next to the grill instead: it holds the
  Bluetooth link and relays the probe over your existing network (Ethernet or
  WiFi) to the host, wherever that lives. This is the feature that makes an
  always-on, self-hosted setup actually practical — see
  **[docs/remote-bridge.md](docs/remote-bridge.md)**.
- **Cook history & smarter ETA** — every cook is saved to SQLite, past cooks
  can be browsed or deleted, and the ETA learns from your own past cooks of the
  same meat type.
- **Home Assistant integration** (via HACS) and **Prometheus metrics** for
  automation and monitoring, with ready-made **[Grafana dashboards](docs/grafana/)**
  for either source.
- **Mock mode** to explore the UI with simulated data — no probe or Bluetooth
  required.

See **[docs/](docs/)** for the full feature list, configuration reference, and
integration guides.

## Requirements

- A charged MEATER probe removed from its charging block (it only advertises
  when out of the block).
- A Bluetooth LE adapter — or an [ESP32 bridge](docs/remote-bridge.md) instead,
  in which case the host itself needs no local Bluetooth at all.
- Go 1.26+ to build from source.

## Quick start

```sh
go run . -mock        # explore the web UI with simulated data
```

Open <http://localhost:8080/> and press **Start** — the app sits idle until you
do, so it never scans in the background. With a real probe, just drop `-mock`:

```sh
go run .              # serve the dashboard on :8080, idle until you press Start
```

Press `Ctrl+C` to disconnect and exit.

## Install

**Docker** — try the UI with no probe needed:

```sh
docker run --rm -p 8080:8080 ghcr.io/awlx/meater:latest -mock -http :8080
```

**Binary + systemd**:

```sh
CGO_ENABLED=0 go build -o meater .
```

Full instructions for both (Docker Compose for a real probe, GHCR images, and
the systemd unit) are in **[docs/install.md](docs/install.md)**.

## Documentation

| Doc                                                  | Covers                                                                            |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------- |
| **[docs/install.md](docs/install.md)**                 | Docker Compose, GHCR images, binary + systemd setup.                              |
| **[docs/configuration.md](docs/configuration.md)**     | The full `-flag` reference.                                                       |
| **[docs/remote-bridge.md](docs/remote-bridge.md)**     | 📡 The ESP32 bridge — read the probe over the network instead of local Bluetooth. |
| **[docs/https.md](docs/https.md)**                     | HTTPS/TLS options, and how multiple viewers share one probe.                      |
| **[docs/home-assistant.md](docs/home-assistant.md)**   | Installing via HACS and the entities it exposes.                                  |
| **[docs/metrics.md](docs/metrics.md)**                 | The Prometheus `/metrics` endpoint and example alert rules.                       |
| **[docs/grafana/](docs/grafana/)**                     | Ready-made Grafana dashboards, for the native `/metrics` or Home Assistant.        |
| **[docs/architecture.md](docs/architecture.md)**       | Project layout and how the BLE temperature payload is decoded.                    |
| **[firmware/](firmware/)**                             | ESP32 bridge firmware (PlatformIO/C++), wiring, and flashing.                      |

## Acknowledgements

Thanks to [`nathanfaber/meaterble`](https://github.com/nathanfaber/meaterble)
for the community reverse-engineering pointers — it was a helpful reference for
where to look in the BLE GATT services and temperature payload.

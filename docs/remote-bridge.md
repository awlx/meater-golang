# Remote probe over a networked ESP32 bridge (`-bridge`)

**The single biggest limitation of a self-hosted MEATER monitor is range: the
host running this program has to be within Bluetooth distance of the probe —
typically a few meters, less through walls.** That's awkward when the grill or
smoker is outside and the server (a NAS, a Raspberry Pi, a home server) lives
indoors in a cupboard or a rack.

The ESP32 bridge solves this: a cheap microcontroller sits *next to the grill*
and holds the Bluetooth link, then forwards the probe's readings over your
existing network (Ethernet or WiFi) to the host running this program, wherever
that is. The host no longer needs to be anywhere near the probe — just on the
same network. This is the recommended setup for anything beyond "the server is
in the same room as the grill."

Two boards are supported:

- A PoE **Olimex ESP32-POE-ISO**, over Ethernet — one cable for power and
  network.
- A generic ESP32 dev board, over WiFi — credentials are collected through a
  captive portal on first boot, no Ethernet hardware needed.

```
MEATER ~BLE~> ESP32-POE-ISO ──PoE/Ethernet──> meater-golang (dashboard, history, ETA)
MEATER ~BLE~> ESP32 dev board ──WiFi────────> meater-golang (dashboard, history, ETA)
```

Firmware, wiring and troubleshooting: **[`firmware/`](../firmware/)**.

```sh
cd firmware && pio run -t upload                  # PoE/Ethernet board (default env)
cd firmware && pio run -e esp32-wifi -t upload    # or: generic dev board over WiFi
cd .. && go build -tags nobluetooth -o meater .
./meater -bridge 192.168.1.42:9000                # IP printed in the board's serial log
```

The bridge is a peer of the local BLE source, not a replacement: `Start` dials
the board, `Stop` hangs up, and everything downstream (decoding, history, ETA,
alerts) is identical. The board forwards the probe's **raw** payload, so
`internal/meater.ParseTemperature` remains the only decoder in the project and
the two transports cannot drift apart.

> **Note:** the ESP32 cannot run this program itself — it is a microcontroller
> with no OS, and `modernc.org/sqlite`, BlueZ/D-Bus and `net/http` all need a
> POSIX host. It is the radio, not the computer.

## Building without a local Bluetooth stack

`-tags nobluetooth` compiles out the local BLE backend, leaving `-bridge` and
`-mock`. It is optional on Linux, but **required on macOS**: macOS aborts
(SIGABRT, a few hundred ms after startup, with no error message) any long-lived
unsigned binary that links CoreBluetooth without a Bluetooth usage description
in a signed app bundle. Importing `tinygo.org/x/bluetooth` is enough to trigger
it, so without the tag the app dies at startup on macOS even in `-bridge` or
`-mock` mode, where Bluetooth is never used.

The same applies to the tests — on macOS, use:

```sh
go test -tags nobluetooth ./...
```

Plain `go test ./...` aborts in the root package there for the same reason (its
test binary links CoreBluetooth). Linux and CI are unaffected.

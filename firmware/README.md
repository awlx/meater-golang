# ESP32 bridge firmware

A thin firmware that lets meater-golang read a MEATER probe through an **Olimex
ESP32-POE-ISO** over Ethernet, for when the grill is out of the server's
Bluetooth range. Pairs with the `-bridge` flag; the Go side lives in
[`../bridge.go`](../bridge.go).

This directory is not part of the Go module or the Docker image — it is
firmware, built with [PlatformIO](https://platformio.org/), and `go build ./...`
ignores it.

## Why not just run meater-golang on the ESP32?

You can't, and it's worth being explicit about why so nobody burns a weekend on it:

- **The ESP32 is a microcontroller, not a computer.** Xtensa LX6, ~520KB SRAM,
  bare-metal/FreeRTOS. There is no OS. Standard Go needs one — there is no
  `GOOS=esp32`.
- **The dependencies are impossible there.** `modernc.org/sqlite` pulls in
  `modernc.org/libc` (a POSIX libc emulation needing a filesystem, mmap and
  threads); `godbus` talks to BlueZ, a Linux daemon; plus `net/http` + SSE.
  TinyGo supports none of that.
- **The tempting dead end:** `tinygo.org/x/bluetooth`'s support table has an
  "ESP32 (NINA-FW)" column. That does **not** mean Go runs on the ESP32. It
  means an ESP32 running `nina_fw` acts as a *BLE radio co-processor* for Go
  running on a **different** host MCU over SPI. ESP32 as peripheral, never as
  the compute target.

So the board does what it's genuinely great at — being a PoE-powered BLE radio
that sits in range of the grill on a single cable — and the Go program runs on a
real host.

```
  MEATER probe  ~BLE~>  ESP32-POE-ISO  ──Ethernet/PoE──>  meater-golang
                        (this firmware)                    (dashboard, SQLite,
                                                            ETA, alerts)
```

## Design notes

**The firmware does not decode temperatures.** It forwards the probe's *raw*
characteristic payload and lets `internal/meater.ParseTemperature` decode it.
That decoder carries calibration validated against the official MEATER app (the
`/32` scale; the ambient sensor at byte offset 10, *not* `data[2:4]`).
Re-implementing it in C++ would fork the project's most fragile logic across two
languages and two release cycles.

**Go is the TCP client.** It dials the bridge when you press Start and hangs up
on Stop, so the board only scans for the probe while someone is actually
watching. That maps the existing Start/Stop contract onto a remote radio with no
extra control channel.

### Wire protocol

ASCII, one `\n`-terminated line per message, port 9000:

| Line | Meaning |
|---|---|
| `T <hex>` | raw temperature payload, hex encoded |
| `S connected` | GATT link to the probe is live |
| `S disconnected` | probe not connected; the bridge keeps rescanning |
| `# <text>` | banner / scan keepalive — content ignored, but see below |

Debuggable with `nc <board-ip> 9000`.

**The bridge guarantees a line at least every ~10s** — a reading, a status
change, or a `#` keepalive while it scans. This is a contract, not a nicety: the
client uses silence to detect a wedged board, so a probe that simply isn't in
range yet must not look like a failure. Without the keepalive the client hangs
up mid-scan and redials forever whenever the probe is absent. If you change the
scan duration, keep it comfortably under the client's 30s watchdog.

## Build & flash

Requires [PlatformIO](https://platformio.org/).

```sh
cd firmware
pio run                    # build
pio run -t upload          # flash over USB
pio device monitor         # serial log @115200
```

Pinned to `espressif32@6.7.0` (Arduino core 2.0.x) deliberately: core 3.x
renamed `WiFiServer`/`WiFi.onEvent` to `NetworkServer`/`Network.onEvent`. The
Ethernet pin map (PHY power on GPIO12, MDC 23, MDIO 18, clock GPIO17) comes from
the core's `esp32-poe-iso` variant header, so `ETH.begin()` takes no arguments
and there are no magic numbers to get wrong.

Current usage: **RAM 16.6%, Flash 54.4%** of the 1.9MB `min_spiffs` app slot.

Verified on real hardware (ESP32-POE-ISO, USB-powered, wired Ethernet): DHCP at
100Mbps full duplex, probe discovered and subscribed, live readings arriving in
the dashboard through the full `MEATER+ -> BLE -> board -> Ethernet -> Go` chain.

## Run

Plug the board into a PoE switch (or USB + regular Ethernet). It takes DHCP and
registers as `meater-bridge`. Find its IP in the serial log:

```
ethernet up: 192.168.1.42 (100Mbps, full duplex)
TCP server ready on :9000 (reachable once ethernet has an IP)
```

Then point meater-golang at it, from the repo root:

```sh
go build -tags nobluetooth -o meater .
./meater -bridge 192.168.1.42:9000
```

### Why `-tags nobluetooth`

The bridge exists so the *host* needs no Bluetooth — so don't link a BLE stack
into it. On macOS this is not merely tidy but **required**: macOS aborts
(SIGABRT, ~100ms after launch, no error message) any long-lived process that
links CoreBluetooth without a Bluetooth usage description in a signed app
bundle. Without the tag, `meater-golang` dies at startup on macOS even in
`-bridge` or `-mock` mode, since importing `tinygo.org/x/bluetooth` is enough to
trigger it. On Linux the tag is optional but still avoids a pointless BlueZ
dependency.

## Troubleshooting

The serial log narrates each stage, so read it top-down:

| Last line you see | Meaning |
|---|---|
| `meater-bridge starting` only | The PHY never initialised — a board-level fault, not config. GPIO12 powers the LAN8720. |
| `ethernet: PHY started` | No link: cable unplugged, or the switch port is dead. |
| `ethernet: link up, requesting DHCP` | Link is fine but no lease — no DHCP server on that VLAN. |
| `waiting for ethernet ...` repeating | Never got an IP. USB supplies power and serial only; the bridge needs a cable. |
| `ethernet up: <ip>` then silence | Working. It's scanning; it only scans while a client is attached. |

| Symptom | Likely cause |
|---|---|
| `S disconnected` forever | The MEATER app (phone) is holding the probe. One BLE central at a time — close the app. |
| Bridge unreachable from Go | Board and host on different VLANs; port 9000 filtered. Test with `nc <ip> 9000`. |
| Go app aborts instantly on macOS | Built without `-tags nobluetooth` (see above). |

Give DHCP a few seconds after reset before assuming Ethernet is broken — link
negotiation plus a lease can outlast a short serial capture.

# Configuration reference

## Flags

| Flag                | Default      | Description                                                                       |
| ------------------- | ------------ | -------------------------------------------------------------------------------- |
| `-addr`             | (none)       | Connect to a specific BLE MAC (e.g. `AA:BB:CC:DD:EE:FF`) instead of matching by name. |
| `-adapter`          | (none)       | Bluetooth controller to use, as an hci id (e.g. `hci1`) or a controller MAC (e.g. `AA:BB:CC:DD:EE:FF`); empty uses `hci0`. A MAC is resolved to its hci id at startup, so it survives adapter re-numbering across reboots (Linux/BlueZ only). |
| `-scan-window`      | `15s`        | How long each scan attempt runs before retrying.                                 |
| `-timeout`          | `0`          | Give up after this long (`0` = retry forever).                                    |
| `-connect-retries`  | `3`          | Connection attempts before rescanning.                                           |
| `-connect-timeout`  | `25s`        | Abort a single connection attempt that hangs (BlueZ can stall).                  |
| `-http`             | `:8080`      | Address for the plain-HTTP server (also serves ACME challenges / HTTPS redirect).|
| `-https`            | `:8443`      | Address for the HTTPS server (used when TLS is enabled).                          |
| `-acme-domain`      | (none)       | Get a Let's Encrypt cert automatically for this domain (e.g. `meater.example.com`). |
| `-acme-cache`       | `acme-certs` | Directory to cache ACME certificates in.                                         |
| `-tls-cert`         | (none)       | Path to a TLS certificate file (use with `-tls-key`).                            |
| `-tls-key`          | (none)       | Path to a TLS private key file (use with `-tls-cert`).                           |
| `-target`           | `63`         | Default target tip temperature in Celsius.                                       |
| `-mock`             | `false`      | Simulate a probe instead of using Bluetooth (for UI testing).                    |
| `-bridge`           | (none)       | Read the probe from a networked ESP32 BLE bridge at `host:port` instead of a local adapter. See [remote-bridge.md](remote-bridge.md). |
| `-db`               | `meater.db`  | SQLite file for cook history (empty string disables persistence).                |
| `-cook-idle`        | `30m`        | Finish the current cook after this long without a reading (covers BLE drops/reconnects). |

The program retries scanning automatically, so you can start it before freeing
the probe and it will connect as soon as the probe begins advertising:

```sh
go run . -addr AA:BB:CC:DD:EE:FF -scan-window 12s
```

See also: [HTTPS/TLS options](https.md), [remote ESP32 bridge](remote-bridge.md).

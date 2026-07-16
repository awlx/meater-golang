# Install

## Option A — Docker (ready to go)

A multi-stage [`Dockerfile`](../Dockerfile) builds a tiny static image, and
[`docker-compose.yml`](../docker-compose.yml) wires up everything for a real probe
on a **Linux** host (it uses the host network and the host D-Bus so the
container can reach BlueZ):

```sh
docker compose up -d --build
# open http://<host>:8080/
```

The image is also published to GHCR by a GitHub Actions workflow
([`.github/workflows/docker-publish.yml`](../.github/workflows/docker-publish.yml))
on every push to `main` and on `v*` tags, so you can pull it instead of
building:

```sh
docker pull ghcr.io/awlx/meater:latest
```

> The compose file references `ghcr.io/awlx/meater:latest`. With `--build` it
> builds locally; without it, Docker pulls from GHCR (make the GHCR package
> public once after the first publish to allow anonymous pulls).

To just try the UI without a probe (works anywhere, including macOS/Windows
Docker Desktop):

```sh
docker run --rm -p 8080:8080 ghcr.io/awlx/meater:latest -mock -http :8080
```

Or build locally instead of pulling, e.g. to test an unreleased change:

```sh
docker build -t meater . && docker run --rm -p 8080:8080 meater -mock -http :8080
```

> BLE inside Docker requires a Linux host with Bluetooth, the host network, and
> access to the host D-Bus system bus (all preconfigured in the compose file).
> If the probe won't connect, uncomment `cap_add` or `privileged` in the compose
> file.

## Option B — Binary + systemd

Build a static binary and install it:

```sh
go install github.com/awlx/meater-golang@latest   # installs to $(go env GOPATH)/bin
# or build locally:
CGO_ENABLED=0 go build -o meater .
sudo install -D -m 0755 meater /opt/meater/meater
```

A sample unit lives at [`deploy/meater.service`](../deploy/meater.service). Copy it
to `/etc/systemd/system/`, adjust `User` and the paths to match your install,
then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now meater.service
```

On Linux, allow the binary to use BLE without running as root:

```sh
sudo setcap 'cap_net_raw,cap_net_admin+eip' /opt/meater/meater
```

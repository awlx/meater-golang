# syntax=docker/dockerfile:1

# ---- build stage -------------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download

# Build a fully static binary. On Linux the BLE backend talks to BlueZ over
# D-Bus (pure Go), so CGO is not required.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/meater .

# ---- runtime stage -----------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/meater /app/meater

# Web UI / API. Map this through to the host (or a reverse proxy) as you like.
EXPOSE 8088

# Default to the web server on :8088. Override the command to add flags, e.g.
#   docker run ... awlx/meater -http :8088 -target 95
# or run without a probe:
#   docker run ... awlx/meater -mock -http :8088
ENTRYPOINT ["/app/meater"]
CMD ["-http", ":8088"]

# HTTPS / TLS

By default the app serves **plain HTTP** — that's perfectly fine on a trusted
home network. Pick whichever of these suits you:

- **Nothing** — plain HTTP on `-http` (default `:8080`). Note: native browser
  notifications and the PWA service worker require a *secure context*, so over
  plain HTTP only the in-page beep/banner alerts work.
- **Built-in ACME** — add `-acme-domain meater.example.com`. The app obtains and
  renews a Let's Encrypt certificate, redirects HTTP→HTTPS, and serves TLS on
  `-https`. Needs the domain to resolve to the host and ports 80/443 reachable.
- **Your own certificate** — `-tls-cert cert.pem -tls-key key.pem` to serve TLS
  with a certificate from any ACME client or CA.
- **Reverse proxy** — keep the app on plain HTTP and let nginx/Caddy/HAProxy/
  Traefik terminate TLS in front of it.

## Multiple viewers and one probe

The web UI supports **multiple simultaneous clients** — every browser gets its
own Server-Sent Events stream, so phones and laptops can all watch the same cook
live.

The *probe* itself is a standard BLE peripheral that accepts **a single
connection**. While your phone (or the MEATER app) is connected, the probe stops
advertising and this program can't discover it. If it reports "no probe found",
close the MEATER app or disable Bluetooth on the phone so the probe advertises
again. Both the original `MEATER` and the long-range `MEATER+` are matched
automatically.

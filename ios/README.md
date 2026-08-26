# ProbePilot for iOS

A native iPhone companion app for the meater-golang server, mirroring the
embedded web dashboard (`internal/server/web`) — same dark look, same cards,
same behaviour — plus the two things only a native app can do well:

- **Live Activities** — the current cook lives on the Lock Screen and in the
  Dynamic Island: internal temperature, target, progress bar, ambient, and a
  live ETA countdown.
- **Home & Lock Screen widgets** — small and medium home-screen widgets and
  circular / rectangular / inline lock-screen accessories showing the probe
  temperature, target, state, and time remaining.

## Features

- Live probe dashboard streamed over the server's `/api/stream` SSE feed:
  progress ring, internal/ambient temperatures, rise rate, state badge
  (waiting / cooking / stalled / ready / disconnected), bridge RSSI pill.
- ETA card with a smooth local countdown, estimated ready wall-clock time,
  and the "based on N past cooks" confidence note.
- Start/Stop cook sessions, name the cook, tag the meat type (with
  suggestions from past cooks, like the web UI's datalist).
- Target doneness presets (same table as the web UI) and custom targets,
  in °C or °F.
- Temperature chart (Swift Charts) with internal/ambient lines, dashed
  target line, and scrub-to-inspect tooltip; tap a past cook to view its
  curve, exactly like the web chart.
- Past-cooks history with delete.
- Ambient-range and "almost done" alerts as local notifications, mirroring
  the web alert card.

## Building

The Xcode project is generated with [XcodeGen](https://github.com/yonaskolb/XcodeGen):

```sh
brew install xcodegen
cd ios
xcodegen generate
open ProbePilot.xcodeproj
```

Then, in Xcode:

1. Select your team under **Signing & Capabilities** for both targets
   (`ProbePilot` and `ProbePilotWidgets`).
2. If your team can't claim the default bundle ids (`dev.awlx.probepilot`
   and `.widgets`), change them on both targets **and** update the app group
   id (`group.dev.awlx.probepilot`) in:
   - `Shared/SharedStore.swift`
   - `ProbePilot/ProbePilot.entitlements`
   - `ProbePilotWidgets/ProbePilotWidgets.entitlements`
3. Build & run on an iPhone (iOS 17+).

On first launch, open **Settings (gear icon)** and enter the server's base
URL, e.g. `http://192.168.1.20:8080`. The URL is stored in the shared app
group so widgets can refresh from the network on their own.

## Architecture

```
ios/
├── project.yml               XcodeGen spec (app + widget extension)
├── Shared/                   Compiled into BOTH targets
│   ├── MeaterModels.swift    Codable mirrors of the Go server's JSON + RFC3339 parsing
│   ├── CookActivityAttributes.swift  ActivityKit payload for the Live Activity
│   ├── SharedStore.swift     App-group storage: widget snapshot, server URL, unit
│   └── Theme.swift           Palette ported from web/styles.css + formatting
├── ProbePilot/                 The app
│   ├── AppModel.swift        Observable state: SSE loop, chart buckets, alerts, actions
│   ├── MeaterAPI.swift       REST + SSE client for /api/*
│   ├── LiveActivityController.swift  Starts/updates/ends the cook activity
│   ├── NotificationManager.swift     Local notifications (ambient + almost-done)
│   └── Views/                Dashboard cards mirroring the web UI
└── ProbePilotWidgets/          Widget extension
    ├── CookStatusWidget.swift   Home/lock-screen widgets (timeline + network refresh)
    └── CookLiveActivity.swift   Lock Screen + Dynamic Island presentation
```

Data flow:

- The app holds one SSE connection to `/api/stream` while foregrounded and
  applies each frame to the dashboard, the Live Activity, and a
  `WidgetSnapshot` written into the app group (then asks WidgetKit to
  redraw).
- Widget timelines first try `GET /api/status` themselves (5 s timeout) and
  fall back to the last snapshot, so widgets stay useful when the phone can
  reach the server but the app hasn't been opened.
- Without a push channel, the Live Activity updates while the app is open
  (foreground); afterwards it keeps the last state and the ETA countdown
  keeps ticking client-side. Server-driven ActivityKit push updates would
  need APNs support in the Go server and are a natural follow-up.

## Releasing to TestFlight

`release.sh` is a self-contained local release chain — no fastlane, no CI,
and no credentials in the repo:

```sh
cd ios
cp release.env.example release.env   # gitignored; fill in your values
./release.sh                         # archive + upload to TestFlight
./release.sh --no-upload             # just produce build/export/*.ipa
```

One-time setup:

1. In App Store Connect, generate a **Team Key** under *Users and Access →
   Integrations → App Store Connect API* (role: App Manager). Put the
   downloaded `AuthKey_<KEYID>.p8` in `~/.appstoreconnect/private_keys/` —
   outside the repo, and the script refuses paths inside it.
2. Fill `release.env` with your Team ID, the Key ID, and the Issuer ID.
3. Create the app record once in App Store Connect (My Apps → **+**) with
   bundle id `dev.awlx.probepilot`.

**Signing model**: Release archives are signed *manually* with App Store
distribution profiles minted by `./provision.py` (ASC API; `release.sh`
runs it automatically when the profiles are missing). This sidesteps
`xcodebuild`'s cloud signing, whose API-key authentication is unreliable
("Authentication failed: Make sure a bearer token was provided…"). The
API key is still used to authenticate the upload itself.

**One-time portal setup** (the public API cannot manage app groups): on
developer.apple.com → *Certificates, Identifiers & Profiles* →
*Identifiers*, register the App Group `group.dev.awlx.probepilot`, then
edit both App IDs (`dev.awlx.probepilot` and `.widgets`) → **App Groups**
→ Configure → assign that group. Until this is done, `provision.py`
refuses to install profiles (they'd come back without the group and the
archive would fail on the application-groups entitlement). You also need
an Apple Distribution certificate in your keychain (Xcode → Settings →
Accounts → Manage Certificates), and its private key only lives there —
back it up.

Everything secret stays out of git by construction: `release.env`, `*.p8`,
archives, ipas, and the generated `ExportOptions.plist` (which embeds the
team ID) are all gitignored, and `release.sh` aborts if `release.env` is
ever tracked or un-ignored. Signing is Xcode cloud signing — the API key
lets `xcodebuild` create/refresh certificates, identifiers, and profiles
automatically, so nothing signing-related is stored in the project.

The build number defaults to `git rev-list --count HEAD`, so each upload is
newer than the last; override with `BUILD_NUMBER=n ./release.sh` if needed.
Bump `MARKETING_VERSION` in `project.yml` (or via `release.env`) for a new
user-facing version.

## Testing against a mock server

`tests/contract_test.py` exercises every endpoint and JSON shape the app
depends on — the Codable field lists, the SSE line framing, the cook/session
lifecycle, and the RFC3339Nano timestamps (the server emits 9 fractional
digits, which `GoJSON.parseDate` trims to milliseconds for Apple's ISO8601
parser) — against a live server:

```sh
go run . -mock -http :8080 -db /tmp/meater-test.db   # from the repo root
python3 ios/tests/contract_test.py
```

For manual testing, point the app's Settings at that mock server; the mock
runs a fast simulated cook, so the ring, chart, ETA, widgets, and Live
Activity all animate within seconds.

## Notes

- ATS is opened up (`NSAllowsArbitraryLoads`) because the server usually
  runs plain HTTP on a LAN. If you serve HTTPS (see `docs/https.md`), you
  can remove that key from both Info.plists.
- iPhone-only by design (`TARGETED_DEVICE_FAMILY = 1`), portrait-only.
- Ambient/almost-done alert thresholds are evaluated in-app from the live
  stream, like the web UI does in the browser; for alerts with the phone
  locked, the Home Assistant integration remains the heavyweight option.

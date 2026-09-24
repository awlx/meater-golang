# App Store Connect metadata for ProbePilot

Copy-paste source for every text field in App Store Connect and TestFlight.
Character limits are noted per field; all values below fit. Trademark rule:
"MEATER" never appears in the name, subtitle, or keywords — only in the
description body as plain compatibility language, with a non-affiliation
disclaimer.

## App name (30 chars max)

```
ProbePilot
```

## Subtitle (30 chars max)

```
BBQ probe monitor & widgets
```

## Promotional text (170 chars max, changeable without review)

```
Your cook on the Lock Screen: live probe temperatures, a ticking ETA, and
home-screen widgets — streamed from your own self-hosted probe server.
```

## Description (4000 chars max)

```
ProbePilot turns your iPhone into a pit-side dashboard for wireless meat-probe
cooks, streamed live from your own self-hosted meater-golang server on your
home network.

No cloud account, no subscription, no vendor app in the middle — your probe
data stays on your network, and ProbePilot simply watches it beautifully.

LIVE ACTIVITIES AND WIDGETS
• The current cook lives on your Lock Screen and in the Dynamic Island:
  internal temperature, target, progress bar, ambient, and a live ETA
  countdown.
• Small and medium home-screen widgets plus circular, rectangular, and
  inline Lock Screen accessories show probe temperature, target, state,
  and time remaining — and refresh on their own over your network.

LIVE DASHBOARD
• Streaming internal and ambient temperatures with a progress ring, rise
  rate, and a state badge: waiting, cooking, stalled, ready, disconnected.
• ETA card with a smooth countdown, the estimated ready time on the clock,
  and a confidence note based on your past cooks.
• Temperature chart with internal and ambient curves, a dashed target
  line, and scrub-to-inspect.

COOK MANAGEMENT
• Start and stop cook sessions, name the cook, and tag the meat type with
  suggestions from your history.
• Doneness presets for common targets, or set a custom target — in °C
  or °F.
• Browse past cooks and revisit any cook's full temperature curve.

ALERTS
• Local notifications when the ambient temperature drifts out of range or
  the cook is almost done.

WHAT YOU NEED
ProbePilot is a companion app: it requires the free, open-source
meater-golang server (github.com/awlx/meater-golang) running on your local
network — for example on a Raspberry Pi or an ESP32 bridge — reading your
wireless meat probe. Compatible with MEATER® wireless probes via that
bridge. Requires iOS 17 or later; iPhone only.

ProbePilot is an independent open-source project. It is not affiliated with,
endorsed by, or sponsored by Apption Labs or Traeger. MEATER is a
trademark of its respective owner.
```

## Keywords (100 chars max, comma-separated)

```
bbq,smoker,grill,meat,thermometer,probe,cook,temperature,brisket,doneness,eta,widget,pit,barbecue
```

Deliberately no "meater" in the keyword field — trademarked terms there are
a common metadata-rejection trigger under guideline 5.2.

## Category

- Primary: Food & Drink
- Secondary: Utilities

## URLs

- Support URL: https://github.com/awlx/meater-golang
- Marketing URL (optional): https://github.com/awlx/meater-golang
- Privacy Policy URL: required — a simple page/README section stating the
  app collects nothing and talks only to the user's own server. Suggested
  text lives at the bottom of this file.

## Age rating

All questionnaire answers "No" → rated 4+.

## App Privacy (privacy "nutrition label")

Data collection: **Data Not Collected**. The app talks only to the
user-configured server on the local network; nothing is sent to the
developer or third parties.

## What's New — version 1.0 (4000 chars max)

```
Initial release: live cook dashboard, Lock Screen Live Activity with
Dynamic Island support, home and Lock Screen widgets, doneness presets,
temperature chart, past-cook history, and ambient/almost-done alerts.
```

## TestFlight — Beta App Description

```
ProbePilot is a companion app for the open-source meater-golang server. To
test it you need the server running on your local network (or its built-in
mock mode: `go run . -mock -http :8080`). Open Settings in the app and
enter the server URL. Please try the Live Activity, widgets, and alerts
during a real or mock cook.
```

## TestFlight — Beta App Feedback Email

```
<your-contact-email>
```

## Privacy policy (suggested text, host at any URL)

```
ProbePilot Privacy Policy

ProbePilot does not collect, store, or transmit any personal data. The app
communicates exclusively with the meater-golang server whose address you
enter in the app's settings, on your own network. Cook history is stored
by that server, under your control. No analytics, tracking, or third-party
services are used. Contact: <your-contact-email>
```

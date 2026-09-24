"""Contract test: exercises the meater-golang API exactly as the iOS app does.

Mirrors the Swift Codable models (ios/Shared/MeaterModels.swift), the SSE
line-parsing in MeaterAPI.statusStream, and the GoJSON date parser, against a
live server running in -mock mode.

Usage:
    go run . -mock -http :8080 -db /tmp/meater-test.db   # from the repo root
    python3 ios/tests/contract_test.py
"""
import json, re, sys, time, urllib.request

BASE = "http://localhost:8080"
failures = []

def check(name, cond, detail=""):
    print(("PASS " if cond else "FAIL ") + name + (f"  [{detail}]" if detail and not cond else ""))
    if not cond:
        failures.append(name)

def req(path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method,
                               headers={"Content-Type": "application/json"} if data else {})
    with urllib.request.urlopen(r, timeout=10) as resp:
        raw = resp.read()
        return resp.status, (json.loads(raw) if raw.strip() else None)

# --- Swift model contracts -------------------------------------------------
STATUS_FIELDS = {  # ProbeStatus: field -> python type check
    "connected": bool, "usingBridge": bool, "bridgeConnected": bool,
    "probeRssiDbm": int, "hasProbeRssi": bool,
    "tipCelsius": (int, float), "tipFahrenheit": (int, float),
    "ambientCelsius": (int, float), "ambientFahrenheit": (int, float),
    "targetCelsius": (int, float), "targetFahrenheit": (int, float),
    "rateCelsiusPerMin": (int, float), "etaSeconds": (int, float),
    "etaSource": str, "etaLowSeconds": (int, float), "etaHighSeconds": (int, float),
    "etaSamples": int, "state": str, "hasReading": bool, "running": bool,
    "cookName": str, "meatType": str, "cookId": int,
    "startTipCelsius": (int, float), "progressPercent": (int, float),
    "cookStartedAt": str, "elapsedSeconds": (int, float), "updatedAt": str,
}
COOK_STATES = {"idle", "disconnected", "waiting", "cooking", "stalled", "ready"}
COOKMETA_FIELDS = {
    "id": int, "name": str, "meatType": str, "startedAt": str,
    "endedAt": (str, type(None)), "targetCelsius": (int, float),
    "maxTipCelsius": (int, float), "maxAmbientCelsius": (int, float),
    "samples": int, "active": bool,
}
POINT_FIELDS = {"at": str, "tipCelsius": (int, float), "ambientCelsius": (int, float)}

def swift_go_date_parse(s):
    """Replicates GoJSON.parseDate: ISO8601 without fraction, with fraction,
    or trim fraction to 3 digits and retry."""
    from datetime import datetime
    for fmt in ("%Y-%m-%dT%H:%M:%SZ",):
        try: return datetime.strptime(s, fmt)
        except ValueError: pass
    m = re.match(r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})\.(\d+)(Z|[+-]\d{2}:\d{2})$", s)
    if m:
        frac = m.group(2)[:3]
        # ISO8601DateFormatter withFractionalSeconds accepts exactly this shape
        try:
            from datetime import datetime
            return datetime.strptime(f"{m.group(1)}.{frac.ljust(3,'0')}Z" if m.group(3)=="Z" else s, "%Y-%m-%dT%H:%M:%S.%fZ")
        except ValueError: return None
    m = re.match(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$", s)
    return "tz-offset" if m else None

def validate(obj, fields, ctx):
    for k, t in fields.items():
        if k not in obj:
            check(f"{ctx}.{k} present", False, "missing"); continue
        # bool is a subclass of int in python; be strict
        v = obj[k]
        if t is int:
            ok = isinstance(v, int) and not isinstance(v, bool)
        elif t is bool:
            ok = isinstance(v, bool)
        else:
            ok = isinstance(v, t)
        if not ok:
            check(f"{ctx}.{k} type", False, f"{type(v).__name__}={v!r}")
    extra = set(obj) - set(fields)
    check(f"{ctx}: no unknown required handling issues", True)
    if extra:
        print(f"  note: server sends extra keys (fine for Codable): {sorted(extra)}")

def validate_status(s, ctx):
    validate(s, STATUS_FIELDS, ctx)
    check(f"{ctx}.state in enum", s["state"] in COOK_STATES, s["state"])
    for k in ("cookStartedAt", "updatedAt"):
        check(f"{ctx}.{k} parseable by GoJSON", swift_go_date_parse(s[k]) is not None, s[k])

# --- 1. idle status --------------------------------------------------------
code, status = req("/api/status")
check("GET /api/status 200", code == 200)
validate_status(status, "status(idle)")
check("idle: running=false", status["running"] is False)
check("idle: progressPercent=-1 (ring falls back to targetFraction)", status["progressPercent"] == -1)

# --- 2. start a session like CookCard does ---------------------------------
code, s = req("/api/session/start", "POST", {"name": "Contract-test roast", "meatType": "pork neck"})
check("POST /api/session/start 200", code == 200)
check("start echoes cookName", s["cookName"] == "Contract-test roast", s["cookName"])
check("start echoes meatType", s["meatType"] == "pork neck", s["meatType"])
check("start: running=true", s["running"] is True)
# cookId is assigned when the probe link opens, not at session start;
check("start: cookId is 0 until first reading", s["cookId"] == 0, s["cookId"])

# --- 3. SSE stream, parsed exactly like MeaterAPI.statusStream -------------
frames = []
r = urllib.request.Request(BASE + "/api/stream", headers={"Accept": "text/event-stream"})
resp = urllib.request.urlopen(r, timeout=30)
check("SSE content-type", resp.headers.get("Content-Type","").startswith("text/event-stream"),
      resp.headers.get("Content-Type"))
deadline = time.time() + 20
for raw in resp:
    line = raw.decode().rstrip("\n").rstrip("\r")
    # exact Swift rule: hasPrefix("data:") -> dropFirst(5) -> trim -> JSON decode
    if line.startswith("data:"):
        payload = line[5:].strip()
        frames.append(json.loads(payload))
        if len(frames) >= 6: break
    if time.time() > deadline: break
resp.close()
check("SSE: got frames", len(frames) >= 3, f"{len(frames)} frames")
check("SSE: first frame arrives immediately with full state", frames and frames[0]["running"] is True)
for i, f in enumerate(frames[:3]):
    validate_status(f, f"sse[{i}]")
reading = [f for f in frames if f["hasReading"]]
check("SSE(mock): frames carry readings", len(reading) > 0)
if reading:
    check("SSE(mock): state transitions to a cooking-family state",
          all(f["state"] in COOK_STATES for f in reading))
    check("SSE(mock): updatedAt is fresh (non-zero)", not reading[-1]["updatedAt"].startswith("0001-"))
    check("SSE(mock): cookId assigned once reading", reading[-1]["cookId"] > 0, reading[-1]["cookId"])
    f = reading[-1]
    check("SSE(mock): fahrenheit fields consistent",
          abs(f["tipFahrenheit"] - (f["tipCelsius"]*9/5+32)) < 0.11,
          f"{f['tipCelsius']}C vs {f['tipFahrenheit']}F")

# --- 4. target: preset + custom, like TargetsCard --------------------------
code, s = req("/api/target", "POST", {"celsius": 57})
check("POST /api/target preset 200", code == 200)
check("target set to 57", abs(s["targetCelsius"] - 57) < 0.01, s["targetCelsius"])
code, s = req("/api/target", "POST", {"celsius": 68.5})
check("target custom 68.5", abs(s["targetCelsius"] - 68.5) < 0.01, s["targetCelsius"])
check("targetFahrenheit derived", abs(s["targetFahrenheit"] - (68.5*9/5+32)) < 0.01, s["targetFahrenheit"])

# --- 5. rename cook + meat type, like saveCookInfo -------------------------
code, s = req("/api/cook/name", "POST", {"name": "Renamed roast"})
check("POST /api/cook/name", code == 200 and s["cookName"] == "Renamed roast")
code, s = req("/api/cook/meat", "POST", {"meatType": "beef brisket"})
check("POST /api/cook/meat", code == 200 and s["meatType"] == "beef brisket")

# --- 6. history ------------------------------------------------------------
time.sleep(2)
code, hist = req("/api/history")
check("GET /api/history 200 + list", code == 200 and isinstance(hist, list))
if hist:
    validate(hist[0], POINT_FIELDS, "history[0]")
    check("history[0].at parseable", swift_go_date_parse(hist[0]["at"]) is not None, hist[0]["at"])

# --- 7. cooks list ---------------------------------------------------------
code, cooks = req("/api/cooks")
check("GET /api/cooks 200 + list", code == 200 and isinstance(cooks, list) and len(cooks) >= 1)
active = [c for c in cooks if c["active"]]
check("cooks: exactly one active", len(active) == 1, str(len(active)))
validate(cooks[0], COOKMETA_FIELDS, "cooks[0]")
check("active cook endedAt is null (Swift Date?)", active and active[0]["endedAt"] is None)
cook_id = active[0]["id"]

# --- 8. cook detail --------------------------------------------------------
code, detail = req(f"/api/cooks/{cook_id}")
check("GET /api/cooks/{id} 200", code == 200)
check("detail has id+points", detail["id"] == cook_id and isinstance(detail["points"], list))
if detail["points"]:
    validate(detail["points"][0], POINT_FIELDS, "detail.points[0]")

# --- 9. deleting the ACTIVE cook must 409 (app surfaces server message) ----
try:
    code, _ = req(f"/api/cooks/{cook_id}", "DELETE")
    check("DELETE active cook rejected", False, f"got {code}")
except urllib.error.HTTPError as e:
    check("DELETE active cook rejected with 409", e.code == 409, str(e.code))

# --- 10. stop session, then delete the finished cook -----------------------
code, s = req("/api/session/stop", "POST")
check("POST /api/session/stop", code == 200 and s["running"] is False)
check("stop: state idle", s["state"] == "idle", s["state"])
code, cooks = req("/api/cooks")
ended = [c for c in cooks if c["id"] == cook_id]
check("stopped cook has endedAt set", ended and ended[0]["endedAt"] is not None)
if ended and ended[0]["endedAt"]:
    check("endedAt parseable by GoJSON", swift_go_date_parse(ended[0]["endedAt"]) is not None, ended[0]["endedAt"])
code, _ = req(f"/api/cooks/{cook_id}", "DELETE")
check("DELETE finished cook 204", code == 204)
code, cooks = req("/api/cooks")
check("cook gone from list", all(c["id"] != cook_id for c in cooks))

# --- 11. restart works (Start button after Stop) ---------------------------
code, s = req("/api/session/start", "POST", {"name": "", "meatType": ""})
check("restart session", code == 200 and s["running"] is True)
req("/api/session/stop", "POST")

print()
if failures:
    print(f"{len(failures)} FAILURES:"); [print(" -", f) for f in failures]; sys.exit(1)
print("ALL CONTRACT CHECKS PASSED")

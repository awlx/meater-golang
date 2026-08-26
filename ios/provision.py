#!/usr/bin/env python3
"""Mint App Store distribution profiles for ProbePilot via the ASC API.

Replaces xcodebuild's cloud signing, which fails to authenticate with API
keys on some Xcode versions. Reads credentials from ios/release.env (never
committed); requires the app group to be associated with both App IDs on
developer.apple.com first (a one-time manual step — the public API cannot
manage app groups).

Usage:  ./provision.py               create/refresh both profiles and install them
        ./provision.py --next-build  print (highest uploaded build number) + 1
"""
import base64
import json
import os
import plistlib
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature

HERE = os.path.dirname(os.path.abspath(__file__))
APP_GROUP = "group.dev.awlx.probepilot"
PROFILES = [
    ("ProbePilot AppStore", "dev.awlx.probepilot"),
    ("ProbePilot Widgets AppStore", "dev.awlx.probepilot.widgets"),
]
INSTALL_DIR = os.path.expanduser("~/Library/MobileDevice/Provisioning Profiles")
API = "https://api.appstoreconnect.apple.com/v1"


def die(msg):
    sys.exit(f"provision.py: {msg}")


def load_env():
    env = {}
    path = os.path.join(HERE, "release.env")
    if not os.path.exists(path):
        die("release.env not found (see release.env.example)")
    for line in open(path):
        m = re.match(r"^([A-Z_]+)=(.*)$", line.strip())
        if m:
            env[m.group(1)] = m.group(2).strip("\"'")
    for k in ("ASC_KEY_ID", "ASC_ISSUER_ID"):
        if k not in env:
            die(f"{k} missing from release.env")
    return env


ENV = load_env()
KEY_PATH = ENV.get("ASC_KEY_PATH") or os.path.expanduser(
    f"~/.appstoreconnect/private_keys/AuthKey_{ENV['ASC_KEY_ID']}.p8")
KEY = serialization.load_pem_private_key(open(KEY_PATH, "rb").read(), password=None)


def b64(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=")


def token():
    header = b64(json.dumps({"alg": "ES256", "kid": ENV["ASC_KEY_ID"], "typ": "JWT"}).encode())
    now = int(time.time())
    payload = b64(json.dumps({"iss": ENV["ASC_ISSUER_ID"], "iat": now, "exp": now + 600,
                              "aud": "appstoreconnect-v1"}).encode())
    signing_input = header + b"." + payload
    der = KEY.sign(signing_input, ec.ECDSA(hashes.SHA256()))
    r, s = decode_dss_signature(der)
    sig = b64(r.to_bytes(32, "big") + s.to_bytes(32, "big"))
    return (signing_input + b"." + sig).decode()


def call(method, path, body=None):
    req = urllib.request.Request(
        API + path, method=method,
        headers={"Authorization": f"Bearer {token()}", "Content-Type": "application/json"},
        data=json.dumps(body).encode() if body else None)
    try:
        resp = urllib.request.urlopen(req)
        raw = resp.read()
        return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        die(f"{method} {path} -> HTTP {e.code}: {e.read().decode()[:400]}")


def next_build():
    apps = call("GET", f"/apps?filter[bundleId]={PROFILES[0][1]}")["data"]
    if not apps:
        die(f"no App Store Connect app for {PROFILES[0][1]}")
    builds = call("GET", f"/builds?filter[app]={apps[0]['id']}"
                         "&sort=-uploadedDate&limit=1&fields[builds]=version")["data"]
    latest = int(builds[0]["attributes"]["version"]) if builds else 0
    print(latest + 1)


def main():
    if "--next-build" in sys.argv:
        next_build()
        return
    # Newest valid distribution certificate (the API can't sort by expiry).
    certs = call("GET", "/certificates?filter[certificateType]=DISTRIBUTION"
                        "&limit=200")["data"]
    if not certs:
        die("no Apple Distribution certificate on the account — create one in "
            "Xcode (Settings > Accounts > Manage Certificates) first")
    newest = max(certs, key=lambda c: c["attributes"]["expirationDate"])
    cert_id = newest["id"]
    print(f"using distribution certificate {cert_id} "
          f"(expires {newest['attributes']['expirationDate'][:10]})")

    for name, bundle in PROFILES:
        # Bundle ID resource.
        bids = call("GET", f"/bundleIds?filter[identifier]={bundle}")["data"]
        bids = [b for b in bids if b["attributes"]["identifier"] == bundle]
        if not bids:
            die(f"bundle id {bundle} not registered")
        bid = bids[0]["id"]

        # Replace any existing profile of the same name.
        for old in call("GET", f"/profiles?filter[name]={urllib.parse.quote(name)}")["data"]:
            call("DELETE", f"/profiles/{old['id']}")
            print(f"deleted old profile {name!r}")

        result = call("POST", "/profiles", {"data": {
            "type": "profiles",
            "attributes": {"name": name, "profileType": "IOS_APP_STORE"},
            "relationships": {
                "bundleId": {"data": {"type": "bundleIds", "id": bid}},
                "certificates": {"data": [{"type": "certificates", "id": cert_id}]},
            }}})
        content = base64.b64decode(result["data"]["attributes"]["profileContent"])

        # Verify the app group made it into the entitlements.
        decoded = subprocess.run(["security", "cms", "-D"], input=content,
                                 capture_output=True, check=True).stdout
        ent = plistlib.loads(decoded)["Entitlements"]
        groups = ent.get("com.apple.security.application-groups", [])
        if APP_GROUP not in groups:
            call("DELETE", f"/profiles/{result['data']['id']}")
            die(f"profile {name!r} came back WITHOUT {APP_GROUP} (got {groups}).\n"
                f"  Associate the app group with {bundle} on developer.apple.com\n"
                f"  (Identifiers > App IDs > {bundle} > App Groups > Configure), then rerun.")

        uuid = plistlib.loads(decoded)["UUID"]
        os.makedirs(INSTALL_DIR, exist_ok=True)
        dest = os.path.join(INSTALL_DIR, f"{uuid}.mobileprovision")
        open(dest, "wb").write(content)
        print(f"installed {name!r} -> {dest} (groups: {groups})")

    print("done — profiles ready for ./release.sh")


if __name__ == "__main__":
    main()

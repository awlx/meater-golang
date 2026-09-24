#!/usr/bin/env bash
# Local TestFlight release chain for ProbePilot.
#
#   ./release.sh              archive + upload to TestFlight
#   ./release.sh --no-upload  archive + export the .ipa into build/export, no upload
#
# Configuration comes from ios/release.env (gitignored; see release.env.example).
# Signing is fully automatic ("cloud signing"): xcodebuild uses the App Store
# Connect API key to create/refresh certificates and profiles on the fly, so no
# team ID, profile, or key ever needs to be committed to the repo.
set -euo pipefail
cd "$(dirname "$0")"

die() { echo "release.sh: $*" >&2; exit 1; }

UPLOAD=1
[[ "${1:-}" == "--no-upload" ]] && UPLOAD=0

# --- Load and sanity-check secrets -----------------------------------------
CONFIG=release.env
if [[ -f "$CONFIG" ]]; then
  # Refuse to continue if the secrets file is tracked or not ignored by git —
  # this is the guard that keeps credentials out of the public repo.
  if git ls-files --error-unmatch "$CONFIG" >/dev/null 2>&1; then
    die "$CONFIG is tracked by git — run 'git rm --cached ios/$CONFIG' and check .gitignore"
  fi
  git check-ignore -q "$CONFIG" || die "$CONFIG is not gitignored — fix .gitignore before releasing"
  # shellcheck source=/dev/null
  set -a  # export everything the config sets, so xcodegen sees DEVELOPMENT_TEAM
  source "$CONFIG"
  set +a
fi

: "${DEVELOPMENT_TEAM:?set DEVELOPMENT_TEAM in ios/release.env (see release.env.example)}"
: "${ASC_KEY_ID:?set ASC_KEY_ID in ios/release.env}"
: "${ASC_ISSUER_ID:?set ASC_ISSUER_ID in ios/release.env}"
ASC_KEY_PATH="${ASC_KEY_PATH:-$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8}"
[[ -f "$ASC_KEY_PATH" ]] || die "API key not found at $ASC_KEY_PATH — download AuthKey_${ASC_KEY_ID}.p8 from App Store Connect and put it there"
case "$ASC_KEY_PATH" in
  "$PWD"/*|../*) die "keep the .p8 outside the repo (e.g. ~/.appstoreconnect/private_keys/), not at $ASC_KEY_PATH" ;;
esac

command -v xcodegen >/dev/null || die "xcodegen not installed (brew install xcodegen)"
command -v xcodebuild >/dev/null || die "xcodebuild not found — install Xcode"

# Signing is MANUAL for Release: profiles are minted by ./provision.py via
# the ASC API (xcodebuild's own cloud-signing auth is unreliable with API
# keys). The API key here is only used to authenticate the upload.
AUTH_ARGS=(
  -authenticationKeyPath "$ASC_KEY_PATH"
  -authenticationKeyID "$ASC_KEY_ID"
  -authenticationKeyIssuerID "$ASC_ISSUER_ID"
)

# Ensure the two named profiles (carrying the app group) are installed;
# mint them if missing or stale.
have_profile() {
  local name=$1 f out
  for f in "$HOME/Library/MobileDevice/Provisioning Profiles/"*.mobileprovision; do
    [[ -f "$f" ]] || continue
    out=$(security cms -D -i "$f" 2>/dev/null)
    if grep -q "<string>$name</string>" <<<"$out" \
       && grep -q "group.dev.awlx.probepilot" <<<"$out"; then
      return 0
    fi
  done
  return 1
}
if ! have_profile "ProbePilot AppStore" || ! have_profile "ProbePilot Widgets AppStore"; then
  echo "==> Distribution profiles missing — minting via provision.py"
  ./provision.py
fi

# --- Versioning -------------------------------------------------------------
# Build number defaults to (highest build already on App Store Connect) + 1,
# falling back to the commit count; override with BUILD_NUMBER=n.
if [[ -z "${BUILD_NUMBER:-}" ]]; then
  BUILD_NUMBER=$(./provision.py --next-build 2>/dev/null) \
    || BUILD_NUMBER=$(git rev-list --count HEAD)
fi
VERSION_ARGS=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
[[ -n "${MARKETING_VERSION:-}" ]] && VERSION_ARGS+=(MARKETING_VERSION="$MARKETING_VERSION")

# --- Generate, archive ------------------------------------------------------
xcodegen generate

ARCHIVE=build/ProbePilot.xcarchive
rm -rf "$ARCHIVE"

echo "==> Archiving (build $BUILD_NUMBER)"
xcodebuild archive \
  -project ProbePilot.xcodeproj \
  -scheme ProbePilot \
  -configuration Release \
  -destination 'generic/platform=iOS' \
  -archivePath "$ARCHIVE" \
  DEVELOPMENT_TEAM="$DEVELOPMENT_TEAM" \
  "${VERSION_ARGS[@]}"

# --- Export / upload --------------------------------------------------------
# ExportOptions.plist is generated into build/ (gitignored) so the team ID
# never lands in a tracked file.
DESTINATION=upload
(( UPLOAD )) || DESTINATION=export
mkdir -p build/export
cat > build/ExportOptions.plist <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>method</key><string>app-store-connect</string>
	<key>destination</key><string>${DESTINATION}</string>
	<key>teamID</key><string>${DEVELOPMENT_TEAM}</string>
	<key>signingStyle</key><string>manual</string>
	<key>signingCertificate</key><string>Apple Distribution</string>
	<key>provisioningProfiles</key>
	<dict>
		<key>dev.awlx.probepilot</key><string>ProbePilot AppStore</string>
		<key>dev.awlx.probepilot.widgets</key><string>ProbePilot Widgets AppStore</string>
	</dict>
	<key>uploadSymbols</key><true/>
	<key>manageAppVersionAndBuildNumber</key><false/>
</dict>
</plist>
PLIST

if (( UPLOAD )); then
  echo "==> Uploading build $BUILD_NUMBER to TestFlight"
else
  echo "==> Exporting .ipa to build/export (no upload)"
fi
xcodebuild -exportArchive \
  -archivePath "$ARCHIVE" \
  -exportOptionsPlist build/ExportOptions.plist \
  -exportPath build/export \
  "${AUTH_ARGS[@]}"

if (( UPLOAD )); then
  echo "==> Done. Build $BUILD_NUMBER is processing on App Store Connect (TestFlight tab)."
else
  echo "==> Done: $(ls build/export/*.ipa 2>/dev/null || echo 'see build/export')"
fi

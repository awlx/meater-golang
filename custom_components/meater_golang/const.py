"""Constants for the self-hosted MEATER Monitor integration."""

from __future__ import annotations

from typing import Final

DOMAIN: Final = "meater_golang"

DEFAULT_PORT: Final = 8080
DEFAULT_NAME: Final = "MEATER Monitor"

# The probe pushes a reading every second or two. Polling that fast would put a
# reading in the recorder every second for a cook measured in hours, so we poll
# at a rate that still tracks a fast steak closely enough to be useful.
DEFAULT_SCAN_INTERVAL: Final = 10
MIN_SCAN_INTERVAL: Final = 1
MAX_SCAN_INTERVAL: Final = 300

CONF_VERIFY_SSL: Final = "verify_ssl"

# Cook states reported by the monitor, mirroring internal/monitor's constants.
STATE_IDLE: Final = "idle"
STATE_DISCONNECTED: Final = "disconnected"
STATE_WAITING: Final = "waiting"
STATE_COOKING: Final = "cooking"
STATE_STALLED: Final = "stalled"
STATE_READY: Final = "ready"

COOK_STATES: Final = [
    STATE_IDLE,
    STATE_DISCONNECTED,
    STATE_WAITING,
    STATE_COOKING,
    STATE_STALLED,
    STATE_READY,
]

# Models that can produce the time-to-target estimate. "none" stands in for the
# monitor's empty string, so the sensor always has a valid enum option.
ETA_SOURCE_NONE: Final = "none"
ETA_SOURCES: Final = ["physics", "history", "blend", ETA_SOURCE_NONE]

# The monitor reports "unknown" as -1 on numeric fields rather than null.
UNKNOWN: Final = -1

# Target temperature bounds accepted by the monitor's /api/target endpoint.
MIN_TARGET_C: Final = 0
MAX_TARGET_C: Final = 300

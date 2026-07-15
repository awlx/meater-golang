"""Shared entity plumbing for the self-hosted MEATER Monitor."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity
from homeassistant.util import dt as dt_util

from .const import DEFAULT_NAME, DOMAIN, UNKNOWN
from .coordinator import MeaterCoordinator


def optional_number(value: Any) -> float | None:
    """Return a numeric field, or None where the monitor means "unknown".

    The API reports unknown numbers as -1 rather than null (ETA during a stall,
    progress before the first reading). Passed through verbatim those would show
    up as a real "-1 seconds" in the UI and poison any statistics, so they must
    become None here.
    """
    if value is None:
        return None
    number = float(value)
    if number <= UNKNOWN:
        return None
    return number


def optional_datetime(value: Any) -> datetime | None:
    """Parse a timestamp field, or return None where the monitor means "unset".

    Go marshals a zero time.Time as "0001-01-01T00:00:00Z", which parses fine
    but is not a date anyone wants on a dashboard, so treat anything implausibly
    old as absent.
    """
    if not value:
        return None
    parsed = dt_util.parse_datetime(str(value))
    if parsed is None or parsed.year < 2000:
        return None
    return dt_util.as_utc(parsed)


class MeaterEntity(CoordinatorEntity[MeaterCoordinator]):
    """Base entity: one device per monitor instance."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: MeaterCoordinator, key: str) -> None:
        """Initialise the entity for a config entry."""
        super().__init__(coordinator)
        entry = coordinator.config_entry
        self._attr_unique_id = f"{entry.entry_id}_{key}"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, entry.entry_id)},
            name=DEFAULT_NAME,
            manufacturer="Apption Labs",
            model="MEATER probe (self-hosted monitor)",
            configuration_url=str(coordinator.client.base_url),
        )

    @property
    def status(self) -> dict[str, Any]:
        """Return the latest status payload."""
        return self.coordinator.data or {}

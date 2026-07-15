"""Binary sensors for the self-hosted MEATER Monitor."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
    BinarySensorEntityDescription,
)
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .const import STATE_READY, STATE_STALLED
from .coordinator import MeaterConfigEntry, MeaterCoordinator
from .entity import MeaterEntity


@dataclass(frozen=True, kw_only=True)
class MeaterBinarySensorDescription(BinarySensorEntityDescription):
    """Describes a MEATER binary sensor."""

    value_fn: Callable[[dict[str, Any]], bool]


BINARY_SENSORS: tuple[MeaterBinarySensorDescription, ...] = (
    MeaterBinarySensorDescription(
        key="ready",
        translation_key="ready",
        value_fn=lambda s: s.get("state") == STATE_READY,
    ),
    MeaterBinarySensorDescription(
        key="cooking",
        translation_key="cooking",
        device_class=BinarySensorDeviceClass.RUNNING,
        value_fn=lambda s: bool(s.get("running")),
    ),
    MeaterBinarySensorDescription(
        key="probe_connected",
        translation_key="probe_connected",
        device_class=BinarySensorDeviceClass.CONNECTIVITY,
        entity_category=EntityCategory.DIAGNOSTIC,
        value_fn=lambda s: bool(s.get("connected")),
    ),
    MeaterBinarySensorDescription(
        key="stalled",
        translation_key="stalled",
        device_class=BinarySensorDeviceClass.PROBLEM,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda s: s.get("state") == STATE_STALLED,
    ),
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: MeaterConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the MEATER binary sensors."""
    coordinator = entry.runtime_data
    async_add_entities(
        MeaterBinarySensor(coordinator, description) for description in BINARY_SENSORS
    )


class MeaterBinarySensor(MeaterEntity, BinarySensorEntity):
    """A binary sensor derived from the status payload."""

    entity_description: MeaterBinarySensorDescription

    def __init__(
        self, coordinator: MeaterCoordinator, description: MeaterBinarySensorDescription
    ) -> None:
        """Initialise the binary sensor."""
        super().__init__(coordinator, description.key)
        self.entity_description = description

    @property
    def is_on(self) -> bool:
        """Return the binary sensor state."""
        return self.entity_description.value_fn(self.status)

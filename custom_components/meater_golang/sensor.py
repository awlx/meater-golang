"""Sensors for the self-hosted MEATER Monitor."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.const import (
    PERCENTAGE,
    EntityCategory,
    UnitOfTemperature,
    UnitOfTime,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback
from homeassistant.util import dt as dt_util

from .const import COOK_STATES, ETA_SOURCE_NONE, ETA_SOURCES
from .coordinator import MeaterConfigEntry, MeaterCoordinator
from .entity import MeaterEntity, optional_datetime, optional_number

# How far the estimated ready time must move before the sensor follows it.
#
# The ETA is recomputed from a noisy rate fit on every poll, so the derived
# clock time jitters by a few seconds even on a perfectly steady cook. Writing
# that through would put a new state in the recorder every poll for the length
# of a brisket and make the dashboard's "ready at" flicker. Only a shift that a
# cook would actually act on is worth showing.
READY_AT_DRIFT = timedelta(seconds=60)


@dataclass(frozen=True, kw_only=True)
class MeaterSensorDescription(SensorEntityDescription):
    """Describes a MEATER sensor."""

    value_fn: Callable[[dict[str, Any]], Any]


SENSORS: tuple[MeaterSensorDescription, ...] = (
    # --- The cook itself -------------------------------------------------
    MeaterSensorDescription(
        key="tip_temperature",
        translation_key="tip_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=1,
        value_fn=lambda s: s.get("tipCelsius") if s.get("hasReading") else None,
    ),
    MeaterSensorDescription(
        key="ambient_temperature",
        translation_key="ambient_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=1,
        value_fn=lambda s: s.get("ambientCelsius") if s.get("hasReading") else None,
    ),
    MeaterSensorDescription(
        key="target_temperature",
        translation_key="target_temperature",
        device_class=SensorDeviceClass.TEMPERATURE,
        native_unit_of_measurement=UnitOfTemperature.CELSIUS,
        suggested_display_precision=1,
        value_fn=lambda s: s.get("targetCelsius"),
    ),
    MeaterSensorDescription(
        key="progress",
        translation_key="progress",
        native_unit_of_measurement=PERCENTAGE,
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=0,
        value_fn=lambda s: optional_number(s.get("progressPercent")),
    ),
    MeaterSensorDescription(
        key="time_to_target",
        translation_key="time_to_target",
        device_class=SensorDeviceClass.DURATION,
        native_unit_of_measurement=UnitOfTime.SECONDS,
        suggested_display_precision=0,
        value_fn=lambda s: optional_number(s.get("etaSeconds")),
    ),
    MeaterSensorDescription(
        key="rise_rate",
        translation_key="rise_rate",
        native_unit_of_measurement=f"{UnitOfTemperature.CELSIUS}/min",
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=2,
        value_fn=lambda s: s.get("rateCelsiusPerMin") if s.get("hasReading") else None,
    ),
    MeaterSensorDescription(
        key="state",
        translation_key="state",
        device_class=SensorDeviceClass.ENUM,
        options=COOK_STATES,
        value_fn=lambda s: s.get("state"),
    ),
    MeaterSensorDescription(
        key="cook_name",
        translation_key="cook_name",
        value_fn=lambda s: s.get("cookName") or None,
    ),
    MeaterSensorDescription(
        key="meat_type",
        translation_key="meat_type",
        value_fn=lambda s: s.get("meatType") or None,
    ),
    MeaterSensorDescription(
        key="cook_started",
        translation_key="cook_started",
        device_class=SensorDeviceClass.TIMESTAMP,
        value_fn=lambda s: optional_datetime(s.get("cookStartedAt")),
    ),
    MeaterSensorDescription(
        key="cook_elapsed",
        translation_key="cook_elapsed",
        device_class=SensorDeviceClass.DURATION,
        native_unit_of_measurement=UnitOfTime.SECONDS,
        suggested_display_precision=0,
        value_fn=lambda s: optional_number(s.get("elapsedSeconds")),
    ),
    # --- Diagnostics: how the estimate was arrived at ---------------------
    MeaterSensorDescription(
        key="eta_low",
        translation_key="eta_low",
        device_class=SensorDeviceClass.DURATION,
        native_unit_of_measurement=UnitOfTime.SECONDS,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        suggested_display_precision=0,
        value_fn=lambda s: optional_number(s.get("etaLowSeconds")),
    ),
    MeaterSensorDescription(
        key="eta_high",
        translation_key="eta_high",
        device_class=SensorDeviceClass.DURATION,
        native_unit_of_measurement=UnitOfTime.SECONDS,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        suggested_display_precision=0,
        value_fn=lambda s: optional_number(s.get("etaHighSeconds")),
    ),
    MeaterSensorDescription(
        key="eta_source",
        translation_key="eta_source",
        device_class=SensorDeviceClass.ENUM,
        options=ETA_SOURCES,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda s: s.get("etaSource") or ETA_SOURCE_NONE,
    ),
    MeaterSensorDescription(
        key="eta_samples",
        translation_key="eta_samples",
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda s: s.get("etaSamples"),
    ),
    MeaterSensorDescription(
        key="cook_id",
        translation_key="cook_id",
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda s: s.get("cookId") or None,
    ),
    MeaterSensorDescription(
        key="last_update",
        translation_key="last_update",
        device_class=SensorDeviceClass.TIMESTAMP,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda s: optional_datetime(s.get("updatedAt")),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: MeaterConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the MEATER sensors."""
    coordinator = entry.runtime_data
    entities: list[MeaterEntity] = [
        MeaterSensor(coordinator, description) for description in SENSORS
    ]
    entities.append(MeaterReadyAtSensor(coordinator))
    async_add_entities(entities)


class MeaterSensor(MeaterEntity, SensorEntity):
    """A sensor reading one field out of the status payload."""

    entity_description: MeaterSensorDescription

    def __init__(
        self, coordinator: MeaterCoordinator, description: MeaterSensorDescription
    ) -> None:
        """Initialise the sensor."""
        super().__init__(coordinator, description.key)
        self.entity_description = description

    @property
    def native_value(self) -> Any:
        """Return the sensor value."""
        return self.entity_description.value_fn(self.status)


class MeaterReadyAtSensor(MeaterEntity, SensorEntity):
    """The wall-clock time the cook is estimated to hit its target.

    This is the number a cook actually plans around ("do I start the sides
    now?"), which a countdown in seconds does not answer without arithmetic.
    """

    _attr_device_class = SensorDeviceClass.TIMESTAMP
    _attr_translation_key = "ready_at"

    def __init__(self, coordinator: MeaterCoordinator) -> None:
        """Initialise the sensor."""
        super().__init__(coordinator, "ready_at")
        self._ready_at: datetime | None = None

    @property
    def native_value(self) -> datetime | None:
        """Return the estimated ready time, held steady against ETA jitter."""
        eta = optional_number(self.status.get("etaSeconds"))
        if eta is None:
            self._ready_at = None
            return None

        estimate = dt_util.utcnow() + timedelta(seconds=eta)
        if self._ready_at is None or abs(estimate - self._ready_at) > READY_AT_DRIFT:
            self._ready_at = estimate
        return self._ready_at

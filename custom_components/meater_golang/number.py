"""Target temperature control for the self-hosted MEATER Monitor."""

from __future__ import annotations

from homeassistant.components.number import NumberDeviceClass, NumberEntity, NumberMode
from homeassistant.const import UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .api import MeaterError
from .const import DOMAIN, MAX_TARGET_C, MIN_TARGET_C
from .coordinator import MeaterConfigEntry, MeaterCoordinator
from .entity import MeaterEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: MeaterConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the target temperature number."""
    async_add_entities([MeaterTargetNumber(entry.runtime_data)])


class MeaterTargetNumber(MeaterEntity, NumberEntity):
    """Sets the target tip temperature."""

    _attr_translation_key = "target"
    _attr_device_class = NumberDeviceClass.TEMPERATURE
    _attr_native_unit_of_measurement = UnitOfTemperature.CELSIUS
    _attr_native_min_value = MIN_TARGET_C
    _attr_native_max_value = MAX_TARGET_C
    _attr_native_step = 0.5
    _attr_mode = NumberMode.BOX

    def __init__(self, coordinator: MeaterCoordinator) -> None:
        """Initialise the number entity."""
        super().__init__(coordinator, "target")

    @property
    def native_value(self) -> float | None:
        """Return the current target."""
        return self.status.get("targetCelsius")

    async def async_set_native_value(self, value: float) -> None:
        """Set a new target and adopt the status the monitor returns."""
        try:
            status = await self.coordinator.client.async_set_target(value)
        except MeaterError as err:
            raise HomeAssistantError(
                translation_domain=DOMAIN,
                translation_key="set_target_failed",
                translation_placeholders={"error": str(err)},
            ) from err
        await self.coordinator.async_apply(status)

"""Cook session control for the self-hosted MEATER Monitor."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchDeviceClass, SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .api import MeaterError
from .const import DOMAIN
from .coordinator import MeaterConfigEntry, MeaterCoordinator
from .entity import MeaterEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: MeaterConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the cook session switch."""
    async_add_entities([MeaterSessionSwitch(entry.runtime_data)])


class MeaterSessionSwitch(MeaterEntity, SwitchEntity):
    """Starts and stops a cook session.

    Turning this on starts probe discovery and opens a fresh cook; turning it
    off ends the cook and stops scanning. Note that starting a session always
    begins a *new* cook, so toggling this mid-smoke splits the session in two.
    """

    _attr_translation_key = "session"
    _attr_device_class = SwitchDeviceClass.SWITCH

    def __init__(self, coordinator: MeaterCoordinator) -> None:
        """Initialise the switch."""
        super().__init__(coordinator, "session")

    @property
    def is_on(self) -> bool:
        """Return whether probe discovery is active."""
        return bool(self.status.get("running"))

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Start a cook session."""
        await self._call("start")

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Stop the cook session."""
        await self._call("stop")

    async def _call(self, action: str) -> None:
        """Run a session command and adopt the status it returns."""
        client = self.coordinator.client
        try:
            if action == "start":
                status = await client.async_start_session()
            else:
                status = await client.async_stop_session()
        except MeaterError as err:
            raise HomeAssistantError(
                translation_domain=DOMAIN,
                translation_key="session_failed",
                translation_placeholders={"action": action, "error": str(err)},
            ) from err
        await self.coordinator.async_apply(status)

"""The self-hosted MEATER Monitor integration.

Talks to a meater-golang instance on the local network, which reads the MEATER
probe over Bluetooth itself. Nothing here goes near the MEATER cloud — for that,
use Home Assistant's built-in `meater` integration instead.
"""

from __future__ import annotations

from homeassistant.const import Platform
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import MeaterClient
from .config_flow import build_url
from .const import CONF_VERIFY_SSL
from .coordinator import MeaterConfigEntry, MeaterCoordinator

PLATFORMS: list[Platform] = [
    Platform.BINARY_SENSOR,
    Platform.NUMBER,
    Platform.SENSOR,
    Platform.SWITCH,
]


async def async_setup_entry(hass: HomeAssistant, entry: MeaterConfigEntry) -> bool:
    """Set up a MEATER Monitor instance from a config entry."""
    session = async_get_clientsession(
        hass, verify_ssl=entry.data.get(CONF_VERIFY_SSL, True)
    )
    client = MeaterClient(session, build_url(dict(entry.data)))

    coordinator = MeaterCoordinator(hass, entry, client)
    await coordinator.async_config_entry_first_refresh()
    entry.runtime_data = coordinator

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    entry.async_on_unload(entry.add_update_listener(async_reload_entry))
    return True


async def async_unload_entry(hass: HomeAssistant, entry: MeaterConfigEntry) -> bool:
    """Unload a config entry."""
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)


async def async_reload_entry(hass: HomeAssistant, entry: MeaterConfigEntry) -> None:
    """Reload the entry so a changed poll interval takes effect immediately."""
    await hass.config_entries.async_reload(entry.entry_id)

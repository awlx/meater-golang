"""Polling coordinator for the self-hosted MEATER Monitor."""

from __future__ import annotations

import logging
from datetime import timedelta
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_SCAN_INTERVAL
from homeassistant.core import HomeAssistant
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed

from .api import MeaterClient, MeaterError
from .const import DEFAULT_SCAN_INTERVAL, DOMAIN

_LOGGER = logging.getLogger(__name__)

type MeaterConfigEntry = ConfigEntry[MeaterCoordinator]


class MeaterCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Polls /api/status and shares the result with every entity."""

    config_entry: MeaterConfigEntry

    def __init__(
        self, hass: HomeAssistant, entry: MeaterConfigEntry, client: MeaterClient
    ) -> None:
        """Initialise the coordinator at the entry's configured interval."""
        self.client = client
        interval = entry.options.get(CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL)
        super().__init__(
            hass,
            _LOGGER,
            config_entry=entry,
            name=DOMAIN,
            update_interval=timedelta(seconds=interval),
        )

    async def _async_update_data(self) -> dict[str, Any]:
        """Fetch the current status."""
        try:
            return await self.client.async_get_status()
        except MeaterError as err:
            raise UpdateFailed(str(err)) from err

    async def async_apply(self, status: dict[str, Any]) -> None:
        """Publish a status returned by a command endpoint.

        The write endpoints return the post-change status, so acting on an
        entity can refresh from that reply directly. Without this a target set
        to 92 °C would snap back to its old value in the UI until the next poll
        came round.
        """
        self.async_set_updated_data(status)

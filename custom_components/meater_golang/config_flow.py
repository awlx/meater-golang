"""Config flow for the self-hosted MEATER Monitor."""

from __future__ import annotations

from typing import Any

import voluptuous as vol
from homeassistant.config_entries import (
    ConfigFlow,
    ConfigFlowResult,
    OptionsFlow,
)
from homeassistant.const import CONF_HOST, CONF_PORT, CONF_SCAN_INTERVAL, CONF_SSL
from homeassistant.core import callback
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.selector import (
    NumberSelector,
    NumberSelectorConfig,
    NumberSelectorMode,
)
from yarl import URL

from .api import MeaterClient, MeaterConnectionError, MeaterError
from .const import (
    CONF_VERIFY_SSL,
    DEFAULT_NAME,
    DEFAULT_PORT,
    DEFAULT_SCAN_INTERVAL,
    DOMAIN,
    MAX_SCAN_INTERVAL,
    MIN_SCAN_INTERVAL,
)
from .coordinator import MeaterConfigEntry

STEP_USER_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_HOST): str,
        vol.Required(CONF_PORT, default=DEFAULT_PORT): vol.Coerce(int),
        vol.Required(CONF_SSL, default=False): bool,
        vol.Required(CONF_VERIFY_SSL, default=True): bool,
    }
)


def build_url(data: dict[str, Any]) -> URL:
    """Return the monitor's base URL for a config entry's data.

    The trailing slash matters: URL.join treats a base without one as a file
    and would drop the last path segment, breaking a monitor hosted under a
    sub-path by a reverse proxy.
    """
    scheme = "https" if data.get(CONF_SSL) else "http"
    return URL.build(
        scheme=scheme, host=data[CONF_HOST], port=data[CONF_PORT], path="/"
    )


class MeaterGolangConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle setting up a MEATER Monitor instance."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the manual host/port step."""
        errors: dict[str, str] = {}

        if user_input is not None:
            host = user_input[CONF_HOST].strip()
            user_input[CONF_HOST] = host

            # One entry per instance: re-adding the same host:port should offer
            # to reconfigure rather than create duplicate entities.
            self._async_abort_entries_match(
                {CONF_HOST: host, CONF_PORT: user_input[CONF_PORT]}
            )
            await self.async_set_unique_id(f"{host}:{user_input[CONF_PORT]}")
            self._abort_if_unique_id_configured()

            session = async_get_clientsession(
                self.hass, verify_ssl=user_input[CONF_VERIFY_SSL]
            )
            client = MeaterClient(session, build_url(user_input))
            try:
                status = await client.async_get_status()
            except MeaterConnectionError:
                errors["base"] = "cannot_connect"
            except MeaterError:
                errors["base"] = "invalid_response"
            else:
                # A reachable HTTP server is not necessarily *this* server. The
                # status payload always carries "state", so its absence means
                # we are pointed at something else and the entities would all
                # be unavailable with no explanation.
                if "state" not in status:
                    errors["base"] = "invalid_response"
                else:
                    return self.async_create_entry(
                        title=f"{DEFAULT_NAME} ({host})", data=user_input
                    )

        return self.async_show_form(
            step_id="user",
            data_schema=self.add_suggested_values_to_schema(
                STEP_USER_SCHEMA, user_input
            ),
            errors=errors,
        )

    @staticmethod
    @callback
    def async_get_options_flow(entry: MeaterConfigEntry) -> MeaterOptionsFlow:
        """Return the options flow."""
        return MeaterOptionsFlow()


class MeaterOptionsFlow(OptionsFlow):
    """Handle the poll-interval option."""

    async def async_step_init(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Manage the options."""
        if user_input is not None:
            return self.async_create_entry(data=user_input)

        current = self.config_entry.options.get(
            CONF_SCAN_INTERVAL, DEFAULT_SCAN_INTERVAL
        )
        return self.async_show_form(
            step_id="init",
            data_schema=vol.Schema(
                {
                    vol.Required(CONF_SCAN_INTERVAL, default=current): NumberSelector(
                        NumberSelectorConfig(
                            min=MIN_SCAN_INTERVAL,
                            max=MAX_SCAN_INTERVAL,
                            step=1,
                            unit_of_measurement="s",
                            mode=NumberSelectorMode.BOX,
                        )
                    )
                }
            ),
        )

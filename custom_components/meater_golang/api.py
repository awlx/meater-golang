"""Thin async client for the meater-golang HTTP API."""

from __future__ import annotations

from typing import Any

import aiohttp
from yarl import URL


class MeaterError(Exception):
    """Base error for the MEATER Monitor API."""


class MeaterConnectionError(MeaterError):
    """The monitor could not be reached."""


class MeaterResponseError(MeaterError):
    """The monitor returned something unusable."""


class MeaterClient:
    """Talks to a meater-golang instance.

    The API is unauthenticated by design (it is meant to sit on a home LAN or
    behind a reverse proxy), so there are no credentials to carry here.
    """

    def __init__(self, session: aiohttp.ClientSession, base_url: URL) -> None:
        """Initialise the client against the monitor's base URL."""
        self._session = session
        self._base_url = base_url

    @property
    def base_url(self) -> URL:
        """Return the monitor's base URL."""
        return self._base_url

    async def async_get_status(self) -> dict[str, Any]:
        """Return the current probe status."""
        return await self._request("get", "api/status")

    async def async_set_target(self, celsius: float) -> dict[str, Any]:
        """Set the target tip temperature in Celsius."""
        return await self._request("post", "api/target", json={"celsius": celsius})

    async def async_start_session(self) -> dict[str, Any]:
        """Begin probe discovery and a fresh cook."""
        return await self._request("post", "api/session/start", json={})

    async def async_stop_session(self) -> dict[str, Any]:
        """Halt probe discovery and end the current cook."""
        return await self._request("post", "api/session/stop", json={})

    async def _request(
        self, method: str, path: str, json: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        """Perform a request and return the decoded JSON body."""
        url = self._base_url.join(URL(path))
        try:
            async with self._session.request(
                method, url, json=json, raise_for_status=True
            ) as resp:
                # The monitor sets Content-Type correctly, but a reverse proxy
                # or a captive portal in front of it may not; decode by content
                # rather than trusting the header, so a misconfigured proxy
                # surfaces as a clear error instead of a mystery.
                return await resp.json(content_type=None)
        except aiohttp.ClientResponseError as err:
            raise MeaterResponseError(
                f"{method.upper()} {path} failed: HTTP {err.status}"
            ) from err
        except (aiohttp.ClientError, TimeoutError) as err:
            raise MeaterConnectionError(f"cannot reach {url}: {err}") from err
        except ValueError as err:
            raise MeaterResponseError(
                f"{method.upper()} {path} returned a non-JSON body"
            ) from err

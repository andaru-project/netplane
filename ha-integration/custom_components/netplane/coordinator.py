# SPDX-License-Identifier: Apache-2.0
"""DataUpdateCoordinator for netplane."""

from __future__ import annotations

from datetime import timedelta
import logging
from typing import Any

from homeassistant.core import HomeAssistant
from homeassistant.exceptions import ConfigEntryAuthFailed
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from netplane_client import NetplaneAuthError, NetplaneClient, NetplaneError

from .const import DEFAULT_SCAN_INTERVAL, DOMAIN

_LOGGER = logging.getLogger(__name__)


class NetplaneCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinates periodic state fetches from a netplane server."""

    def __init__(self, hass: HomeAssistant, client: NetplaneClient) -> None:
        super().__init__(
            hass,
            _LOGGER,
            name=DOMAIN,
            update_interval=timedelta(seconds=DEFAULT_SCAN_INTERVAL),
        )
        self.client = client

    async def _async_update_data(self) -> dict[str, Any]:
        try:
            return await self.client.get_state()
        except NetplaneAuthError as err:
            raise ConfigEntryAuthFailed(str(err)) from err
        except NetplaneError as err:
            raise UpdateFailed(str(err)) from err

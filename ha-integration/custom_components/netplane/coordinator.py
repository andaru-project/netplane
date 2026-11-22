# SPDX-License-Identifier: Apache-2.0
"""DataUpdateCoordinator for netplane."""

from __future__ import annotations

from collections.abc import Callable, Iterable
from datetime import timedelta
import logging
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.entity import Entity
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.exceptions import ConfigEntryAuthFailed
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from netplane_client import NetplaneAuthError, NetplaneClient, NetplaneError

from .const import DEFAULT_SCAN_INTERVAL, DOMAIN

_LOGGER = logging.getLogger(__name__)


class NetplaneCoordinator(DataUpdateCoordinator[dict[str, Any]]):
    """Coordinates periodic state fetches from a netplane server."""

    def __init__(self, hass: HomeAssistant, client: NetplaneClient, entry_id: str) -> None:
        super().__init__(
            hass,
            _LOGGER,
            name=DOMAIN,
            update_interval=timedelta(seconds=DEFAULT_SCAN_INTERVAL),
        )
        self.client = client
        # Prefix for entity unique IDs, so two netplane servers never collide.
        self.entry_id = entry_id

    async def _async_update_data(self) -> dict[str, Any]:
        try:
            return await self.client.get_state()
        except NetplaneAuthError as err:
            raise ConfigEntryAuthFailed(str(err)) from err
        except NetplaneError as err:
            raise UpdateFailed(str(err)) from err


@callback
def async_add_discovered(
    entry: ConfigEntry,
    coordinator: NetplaneCoordinator,
    async_add_entities: AddEntitiesCallback,
    build: Callable[[dict[str, Any]], Iterable[Entity]],
) -> None:
    """Add the entities build() derives from coordinator data, now and after every update.

    Entities whose unique ID was already added are skipped, so devices, clients and
    optional readings that first appear after setup still get entities.
    """
    known: set[str | None] = set()

    @callback
    def _discover() -> None:
        new = [e for e in build(coordinator.data or {}) if e.unique_id not in known]
        known.update(e.unique_id for e in new)
        if new:
            async_add_entities(new)

    _discover()
    entry.async_on_unload(coordinator.async_add_listener(_discover))

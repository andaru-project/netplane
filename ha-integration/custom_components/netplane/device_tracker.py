# SPDX-License-Identifier: Apache-2.0
"""Device tracker entities for netplane observed clients."""

from __future__ import annotations

from typing import Any

from homeassistant.components.device_tracker import ScannerEntity, SourceType
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import NetplaneCoordinator, async_add_discovered


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    coordinator: NetplaneCoordinator = hass.data[DOMAIN][entry.entry_id]

    def build(data: dict[str, Any]) -> list[NetplaneClientTracker]:
        macs = dict.fromkeys(c["mac"] for c in data.get("clients", []) if c.get("mac"))  # ordered, unique
        return [NetplaneClientTracker(coordinator, mac) for mac in macs]

    async_add_discovered(entry, coordinator, async_add_entities, build)


class NetplaneClientTracker(CoordinatorEntity[NetplaneCoordinator], ScannerEntity):
    """Represents one wired or wireless client tracked by netplane."""

    def __init__(self, coordinator: NetplaneCoordinator, mac: str) -> None:
        super().__init__(coordinator)
        self._mac = mac
        self._attr_unique_id = f"{coordinator.entry_id}_client_{mac}"

    def _client(self) -> dict[str, Any] | None:
        for c in (self.coordinator.data or {}).get("clients", []):
            if c.get("mac") == self._mac:
                return c
        return None

    @property
    def name(self) -> str:
        c = self._client() or {}
        return c.get("hostname") or self._mac

    @property
    def source_type(self) -> SourceType:
        return SourceType.ROUTER

    @property
    def is_connected(self) -> bool:
        c = self._client()
        return bool(c and c.get("online"))

    @property
    def mac_address(self) -> str:
        return self._mac

    @property
    def ip_address(self) -> str | None:
        c = self._client() or {}
        ips = c.get("ips") or []
        return ips[0] if ips else None

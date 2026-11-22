# SPDX-License-Identifier: Apache-2.0
"""Base entity for netplane entities that belong to one managed device."""

from __future__ import annotations

from typing import Any

from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import NetplaneCoordinator


class NetplaneDeviceEntity(CoordinatorEntity[NetplaneCoordinator]):
    _attr_has_entity_name = True

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator)
        self._device_id = device_id

    def _device(self) -> dict[str, Any] | None:
        for d in (self.coordinator.data or {}).get("devices", []):
            if d.get("id") == self._device_id:
                return d
        return None

    @property
    def available(self) -> bool:
        # A device that stopped answering keeps its last readings on the server; do not report them as current.
        d = self._device()
        return super().available and d is not None and bool(d.get("online"))

    @property
    def device_info(self) -> DeviceInfo:
        d = self._device() or {}
        return DeviceInfo(
            identifiers={(DOMAIN, self._device_id)},
            name=d.get("name") or self._device_id,
            manufacturer=d.get("vendor") or d.get("driver"),
            model=d.get("model"),
            sw_version=(d.get("firmware") or {}).get("current"),
        )

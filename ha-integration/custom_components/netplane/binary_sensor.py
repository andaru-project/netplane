# SPDX-License-Identifier: Apache-2.0
"""Binary sensors for netplane (device reachability, WAN gateways, SFP optical alarms)."""

from __future__ import annotations

from typing import Any

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddEntitiesCallback

from .const import DOMAIN
from .coordinator import NetplaneCoordinator, async_add_discovered
from .entity import NetplaneDeviceEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    coordinator: NetplaneCoordinator = hass.data[DOMAIN][entry.entry_id]

    def build(data: dict[str, Any]) -> list[BinarySensorEntity]:
        entities: list[BinarySensorEntity] = []
        for dev in data.get("devices", []):
            entities.append(NetplaneDeviceOnlineSensor(coordinator, dev["id"]))
            entities.append(NetplaneSfpAlarmSensor(coordinator, dev["id"]))
        for gw in data.get("gateways", []):
            entities.append(NetplaneGatewayOnlineSensor(coordinator, gw["device_id"], gw["name"]))
        return entities

    async_add_discovered(entry, coordinator, async_add_entities, build)


class NetplaneDeviceOnlineSensor(NetplaneDeviceEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{coordinator.entry_id}_{device_id}_online"
        self._attr_name = "Online"

    @property
    def available(self) -> bool:
        # Stays available so it can report the device going offline.
        return self.coordinator.last_update_success

    @property
    def is_on(self) -> bool | None:
        d = self._device()
        return bool(d.get("online")) if d is not None else None


class NetplaneSfpAlarmSensor(NetplaneDeviceEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.PROBLEM

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{coordinator.entry_id}_{device_id}_sfp_alarm"
        self._attr_name = "SFP Optical Alarm"

    @property
    def is_on(self) -> bool:
        for link in (self.coordinator.data or {}).get("links", []):
            if link.get("device_id") != self._device_id:
                continue
            sfp = link.get("sfp") or {}
            if sfp.get("present") and sfp.get("alert_state") in ("warn", "crit"):
                return True
        return False


class NetplaneGatewayOnlineSensor(NetplaneDeviceEntity, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str, gw_name: str) -> None:
        super().__init__(coordinator, device_id)
        self._gw_name = gw_name
        self._attr_unique_id = f"{coordinator.entry_id}_{device_id}_gw_{gw_name}_online"
        self._attr_name = f"Gateway {gw_name}"

    @property
    def is_on(self) -> bool | None:
        for gw in (self.coordinator.data or {}).get("gateways", []):
            if gw.get("device_id") == self._device_id and gw.get("name") == self._gw_name:
                return bool(gw.get("online"))
        return None

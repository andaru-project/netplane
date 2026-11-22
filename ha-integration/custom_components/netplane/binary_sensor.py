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
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddEntitiesCallback
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import DOMAIN
from .coordinator import NetplaneCoordinator


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddEntitiesCallback,
) -> None:
    coordinator: NetplaneCoordinator = hass.data[DOMAIN][entry.entry_id]
    data = coordinator.data or {}
    entities: list[BinarySensorEntity] = []

    for dev in data.get("devices", []):
        entities.append(NetplaneDeviceOnlineSensor(coordinator, dev["id"]))
        entities.append(NetplaneSfpAlarmSensor(coordinator, dev["id"]))

    for gw in data.get("gateways", []):
        entities.append(NetplaneGatewayOnlineSensor(coordinator, gw["device_id"], gw["name"]))

    async_add_entities(entities)


class _NetplaneDeviceBase(CoordinatorEntity[NetplaneCoordinator]):
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
    def device_info(self) -> DeviceInfo:
        d = self._device() or {}
        return DeviceInfo(
            identifiers={(DOMAIN, self._device_id)},
            name=d.get("name") or self._device_id,
            manufacturer=d.get("vendor") or d.get("driver"),
            model=d.get("model"),
            sw_version=(d.get("firmware") or {}).get("current"),
        )


class NetplaneDeviceOnlineSensor(_NetplaneDeviceBase, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{device_id}_online"
        self._attr_name = "Online"

    @property
    def is_on(self) -> bool | None:
        d = self._device()
        return bool(d.get("online")) if d is not None else None


class NetplaneSfpAlarmSensor(_NetplaneDeviceBase, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.PROBLEM

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{device_id}_sfp_alarm"
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


class NetplaneGatewayOnlineSensor(_NetplaneDeviceBase, BinarySensorEntity):
    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str, gw_name: str) -> None:
        super().__init__(coordinator, device_id)
        self._gw_name = gw_name
        self._attr_unique_id = f"{device_id}_gw_{gw_name}_online"
        self._attr_name = f"Gateway {gw_name}"

    @property
    def is_on(self) -> bool | None:
        for gw in (self.coordinator.data or {}).get("gateways", []):
            if gw.get("device_id") == self._device_id and gw.get("name") == self._gw_name:
                return bool(gw.get("online"))
        return None

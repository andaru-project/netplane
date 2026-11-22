# SPDX-License-Identifier: Apache-2.0
"""Sensors for netplane (PoE draw, CPU utilization, gateway latency, active client count)."""

from __future__ import annotations

from typing import Any

from homeassistant.components.sensor import SensorDeviceClass, SensorEntity, SensorStateClass
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import PERCENTAGE, UnitOfPower, UnitOfTime
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
    entities: list[SensorEntity] = [NetplaneActiveClientsSensor(coordinator, entry.entry_id)]

    for dev in data.get("devices", []):
        if dev.get("poe_budget"):
            entities.append(NetplanePoEWattsSensor(coordinator, dev["id"]))
        entities.append(NetplaneCPUSensor(coordinator, dev["id"]))

    for gw in data.get("gateways", []):
        if gw.get("rtt_ms") is not None:
            entities.append(NetplaneGatewayRTTSensor(coordinator, gw["device_id"], gw["name"]))

    async_add_entities(entities)


class NetplaneActiveClientsSensor(CoordinatorEntity[NetplaneCoordinator], SensorEntity):
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_name = "netplane Active Clients"

    def __init__(self, coordinator: NetplaneCoordinator, entry_id: str) -> None:
        super().__init__(coordinator)
        self._attr_unique_id = f"{entry_id}_active_clients"

    @property
    def native_value(self) -> int:
        clients = (self.coordinator.data or {}).get("clients", [])
        return sum(1 for c in clients if c.get("online"))


class _DeviceSensorBase(CoordinatorEntity[NetplaneCoordinator], SensorEntity):
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
        )


class NetplanePoEWattsSensor(_DeviceSensorBase):
    _attr_device_class = SensorDeviceClass.POWER
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_native_unit_of_measurement = UnitOfPower.WATT

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{device_id}_poe_watts"
        self._attr_name = "PoE Power Draw"

    @property
    def native_value(self) -> float | None:
        d = self._device()
        budget = (d or {}).get("poe_budget")
        return float(budget["used_watts"]) if budget else None


class NetplaneCPUSensor(_DeviceSensorBase):
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_native_unit_of_measurement = PERCENTAGE

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str) -> None:
        super().__init__(coordinator, device_id)
        self._attr_unique_id = f"{device_id}_cpu"
        self._attr_name = "CPU Usage"

    @property
    def native_value(self) -> float | None:
        d = self._device()
        stats = (d or {}).get("system_stats") or {}
        val = stats.get("cpu_pct")
        return float(val) if val is not None else None


class NetplaneGatewayRTTSensor(_DeviceSensorBase):
    _attr_device_class = SensorDeviceClass.DURATION
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_native_unit_of_measurement = UnitOfTime.MILLISECONDS

    def __init__(self, coordinator: NetplaneCoordinator, device_id: str, gw_name: str) -> None:
        super().__init__(coordinator, device_id)
        self._gw_name = gw_name
        self._attr_unique_id = f"{device_id}_gw_{gw_name}_rtt"
        self._attr_name = f"Gateway {gw_name} RTT"

    @property
    def native_value(self) -> float | None:
        for gw in (self.coordinator.data or {}).get("gateways", []):
            if gw.get("device_id") == self._device_id and gw.get("name") == self._gw_name:
                rtt = gw.get("rtt_ms")
                return float(rtt) if rtt is not None else None
        return None

# SPDX-License-Identifier: Apache-2.0
"""Async Python client for the netplane HTTP/SSE API."""

from __future__ import annotations

from typing import Any
from urllib.parse import quote

import aiohttp


class NetplaneError(Exception):
    """Base error raised by NetplaneClient."""


class NetplaneAuthError(NetplaneError):
    """Raised when the netplane server rejects authentication (HTTP 401/403)."""


class NetplaneClient:
    """Thin async client talking strictly to netplane's documented HTTP API."""

    def __init__(
        self,
        base_url: str,
        session: aiohttp.ClientSession,
        api_token: str | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._session = session
        self._api_token = api_token

    def _headers(self) -> dict[str, str]:
        headers = {"Accept": "application/json"}
        if self._api_token:
            headers["Authorization"] = f"Bearer {self._api_token}"
        return headers

    async def _get(self, path: str) -> Any:
        url = f"{self._base_url}/api/v1{path}"
        async with self._session.get(url, headers=self._headers()) as resp:
            if resp.status in (401, 403):
                raise NetplaneAuthError(f"Authentication failed (HTTP {resp.status})")
            if resp.status >= 400:
                raise NetplaneError(f"GET {path} failed with HTTP {resp.status}")
            return await resp.json()

    async def _post(self, path: str, payload: dict[str, Any] | None = None) -> Any:
        url = f"{self._base_url}/api/v1{path}"
        async with self._session.post(url, headers=self._headers(), json=payload) as resp:
            if resp.status in (401, 403):
                raise NetplaneAuthError(f"Authentication failed (HTTP {resp.status})")
            if resp.status >= 400:
                raise NetplaneError(f"POST {path} failed with HTTP {resp.status}")
            return await resp.json()

    async def get_health(self) -> dict[str, Any]:
        return await self._get("/health")

    async def get_state(self) -> dict[str, Any]:
        return await self._get("/state")

    async def list_devices(self) -> list[dict[str, Any]]:
        data = await self._get("/devices")
        return list(data.get("items", []))

    async def get_device(self, device_id: str) -> dict[str, Any]:
        return await self._get(f"/devices/{quote(device_id, safe='')}")

    async def get_topology(self) -> dict[str, Any]:
        return await self._get("/topology")

    async def list_clients(self, online_only: bool = False) -> list[dict[str, Any]]:
        qs = "?online=true" if online_only else ""
        data = await self._get(f"/clients{qs}")
        return list(data.get("items", []))

    async def run_diagnostic(
        self,
        kind: str,
        target: str,
        device_id: str | None = None,
    ) -> dict[str, Any]:
        payload: dict[str, Any] = {"kind": kind, "target": target}
        if device_id:
            payload["device_id"] = device_id
        return await self._post("/diagnostics", payload)

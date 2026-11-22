# SPDX-License-Identifier: Apache-2.0
"""Config flow for netplane."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import aiohttp
from homeassistant import config_entries
from homeassistant.const import CONF_URL
from homeassistant.data_entry_flow import FlowResult
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from netplane_client import NetplaneAuthError, NetplaneClient, NetplaneError
import voluptuous as vol

from .const import CONF_API_TOKEN, DOMAIN

USER_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_URL, default="http://127.0.0.1:8099"): str,
        vol.Optional(CONF_API_TOKEN, default=""): str,
    }
)
REAUTH_SCHEMA = vol.Schema({vol.Optional(CONF_API_TOKEN, default=""): str})


class NetplaneConfigFlow(config_entries.ConfigFlow, domain=DOMAIN):
    """Handle a config flow for netplane."""

    VERSION = 1

    async def _probe(self, url: str, token: str | None) -> str | None:
        """Return an error key, or None when the server answers with this token."""
        client = NetplaneClient(url, async_get_clientsession(self.hass), api_token=token)
        try:
            await client.get_health()
            await client.list_devices()
        except NetplaneAuthError:
            return "invalid_auth"
        except (NetplaneError, aiohttp.ClientError, TimeoutError):
            return "cannot_connect"
        return None

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> FlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            url = user_input[CONF_URL].rstrip("/")
            token = user_input.get(CONF_API_TOKEN) or None
            if error := await self._probe(url, token):
                errors["base"] = error
            else:
                await self.async_set_unique_id(url)
                self._abort_if_unique_id_configured()
                return self.async_create_entry(
                    title=f"netplane ({url})",
                    data={CONF_URL: url, CONF_API_TOKEN: token or ""},
                )

        return self.async_show_form(
            step_id="user",
            data_schema=USER_SCHEMA,
            errors=errors,
        )

    async def async_step_reauth(self, entry_data: Mapping[str, Any]) -> FlowResult:
        """Start reauthentication after the server rejected the stored token."""
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> FlowResult:
        """Ask for a new token and update the existing entry with it."""
        entry = self._get_reauth_entry()
        errors: dict[str, str] = {}
        if user_input is not None:
            token = user_input.get(CONF_API_TOKEN) or None
            if error := await self._probe(entry.data[CONF_URL], token):
                errors["base"] = error
            else:
                return self.async_update_reload_and_abort(
                    entry, data_updates={CONF_API_TOKEN: token or ""}
                )

        return self.async_show_form(
            step_id="reauth_confirm",
            data_schema=REAUTH_SCHEMA,
            description_placeholders={"url": entry.data[CONF_URL]},
            errors=errors,
        )

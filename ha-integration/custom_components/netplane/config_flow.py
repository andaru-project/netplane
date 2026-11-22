# SPDX-License-Identifier: Apache-2.0
"""Config flow for netplane."""

from __future__ import annotations

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


class NetplaneConfigFlow(config_entries.ConfigFlow, domain=DOMAIN):
    """Handle a config flow for netplane."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> FlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            url = user_input[CONF_URL].rstrip("/")
            token = user_input.get(CONF_API_TOKEN) or None
            session = async_get_clientsession(self.hass)
            client = NetplaneClient(url, session, api_token=token)
            try:
                await client.get_health()
                await client.list_devices()
            except NetplaneAuthError:
                errors["base"] = "invalid_auth"
            except (NetplaneError, aiohttp.ClientError):
                errors["base"] = "cannot_connect"
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

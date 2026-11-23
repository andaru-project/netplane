<!-- SPDX-License-Identifier: Apache-2.0 -->
# netplane-client

Async Python client for the [Andaru Netplane](https://github.com/andaru-project/netplane) HTTP API, used by its Home Assistant integration.

```python
import aiohttp
from netplane_client import NetplaneClient

async with aiohttp.ClientSession() as session:
    client = NetplaneClient("http://netplane.local:8099", session, api_token="...")
    devices = await client.list_devices()
```

It raises `NetplaneAuthError` when the server rejects the token and `NetplaneError` for any other failed request.

Licensed under Apache-2.0.

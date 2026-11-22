# SPDX-License-Identifier: Apache-2.0
"""Unit tests for netplane_client using a local stdlib HTTP mock server."""

from __future__ import annotations

import asyncio
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import threading
import unittest

import aiohttp

from netplane_client import NetplaneAuthError, NetplaneClient


class _MockHandler(BaseHTTPRequestHandler):
    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        return

    def _check_auth(self) -> bool:
        if self.path == "/api/v1/health":
            return True
        if self.headers.get("Authorization") != "Bearer test-token":
            self.send_response(401)
            self.end_headers()
            return False
        return True

    def _send_json(self, obj: object) -> None:
        raw = json.dumps(obj).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:
        if not self._check_auth():
            return
        if self.path == "/api/v1/health":
            self._send_json({"status": "ok"})
        elif self.path == "/api/v1/devices":
            self._send_json({"items": [{"id": "sw1", "name": "LG510S", "online": True}]})
        elif self.path == "/api/v1/state":
            self._send_json({"devices": [{"id": "sw1"}], "clients": []})
        elif self.path.startswith("/api/v1/clients"):
            self._send_json({"items": [{"mac": "de:ad:be:ef:00:01", "online": True}]})
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self) -> None:
        if not self._check_auth():
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length))
        self._send_json({"id": "diag-1", "kind": body["kind"], "target": body["target"], "ok": True})


class TestNetplaneClient(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), _MockHandler)
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def test_client_endpoints_and_auth(self) -> None:
        async def _run() -> None:
            url = f"http://127.0.0.1:{self.port}"
            async with aiohttp.ClientSession() as session:
                bad = NetplaneClient(url, session, api_token="wrong")
                health = await bad.get_health()
                self.assertEqual(health["status"], "ok")
                with self.assertRaises(NetplaneAuthError):
                    await bad.list_devices()

                ok = NetplaneClient(url, session, api_token="test-token")
                devs = await ok.list_devices()
                self.assertEqual(len(devs), 1)
                self.assertEqual(devs[0]["name"], "LG510S")
                clients = await ok.list_clients(online_only=True)
                self.assertEqual(clients[0]["mac"], "de:ad:be:ef:00:01")
                diag = await ok.run_diagnostic("fdb_lookup", "de:ad:be:ef:00:01")
                self.assertTrue(diag["ok"])

        asyncio.run(_run())


if __name__ == "__main__":
    unittest.main()

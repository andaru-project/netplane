#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
set -eu

CFG="/config/netplane.yaml"
if [ -f /data/options.json ]; then
  OPT_CFG=$(sed -n 's/.*"config_file"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' /data/options.json | head -n 1 || true)
  if [ -n "$OPT_CFG" ]; then
    CFG="$OPT_CFG"
  fi
fi

if [ -f "$CFG" ]; then
  exec /usr/local/bin/netplane serve --config "$CFG" --db /data/netplane.db --listen :8099
fi

exec /usr/local/bin/netplane serve --db /data/netplane.db --listen :8099

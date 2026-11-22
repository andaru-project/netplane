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

OPTS=""
if [ -f /data/options.json ]; then
  OPTS="--ha-options /data/options.json"
fi

if [ -f "$CFG" ]; then
  # shellcheck disable=SC2086
  exec /usr/local/bin/netplane serve $OPTS --config "$CFG" --db /data/netplane.db --listen :8099
fi

# shellcheck disable=SC2086
exec /usr/local/bin/netplane serve $OPTS --db /data/netplane.db --listen :8099

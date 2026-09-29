#!/usr/bin/env bash
set -euo pipefail

# The daemon reads the runtime copy, not the example configuration. Respect the
# adapter's environment override just as its CLI loader does.
adapter_id="${HEARTH_ADAPTER_ZWAVEJS_ADAPTER_ID:-}"
if [[ -z "$adapter_id" ]]; then
  config=.data/simulator-stack/zwavejs.yaml
  line="$(sed -n '/^adapter_id:/p' "$config")"
  line="${line#adapter_id:}"
  line="${line%%#*}"
  adapter_id="$(printf '%s' "$line" | tr -d '[:space:]\047\042')"
fi
[[ "$adapter_id" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || exit 1
curl -fsS "http://127.0.0.1:$SIM_CORE_PORT/v1/adapters/$adapter_id" >/dev/null

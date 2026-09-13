#!/usr/bin/env python3
"""Stop only the real-device validation environment recorded by this worktree."""

import json
import os
import subprocess
import sys

from start import (ROOT, SERVE_MOUNT, STATE_RELPATH, herdr, serve_route, serve_route_matches,
                   tailscale_serve, tailscale_serve_status)

FAULT = "real-device-stop"


def remove_owned_serve_route(state):
    """Remove the tailnet route this record owns, refusing one that changed.

    Returns True when the recorded route was removed. Records written before
    Serve ownership existed have no `serve` key and never touch the shared
    Serve config, so a manual route on the same port survives untouched.

    The status read is a preflight, not an atomic guard: `tailscale serve`
    re-reads the config, so a replacement that lands after this read is removed
    by the command's own write. Pinning `--set-path SERVE_MOUNT` bounds the
    removal to this workflow's root handler, and a replacement that kept a
    different host or a non-root mount makes the command fail closed.
    """
    owned = state.get("serve")
    if not owned:
        return False
    try:
        route = serve_route(tailscale_serve_status(FAULT))
    except RuntimeError as error:
        raise RuntimeError(f"{error}; nothing closed; retry once Tailscale is reachable")
    if route["kind"] == "absent":
        return False
    if not serve_route_matches(route, owned.get("host"), owned.get("proxy")):
        raise RuntimeError(f"{FAULT}: tailnet route {owned.get('url')} was changed "
                           f"({json.dumps(route, sort_keys=True)}); refusing to remove it; nothing closed; "
                           f"delete {STATE_RELPATH} after inspecting `tailscale serve status`")
    tailscale_serve("--set-path", SERVE_MOUNT, "off", fault=FAULT)
    return True


def stop():
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError("real-device-stop: run inside Herdr")
    state_path = ROOT / STATE_RELPATH
    if not state_path.exists():
        print("real-device-stop: no recorded environment; nothing closed")
        return
    state = json.loads(state_path.read_text())
    if state["worktree"] != str(ROOT):
        raise RuntimeError("real-device-stop: recorded worktree mismatch; nothing closed")
    tabs = herdr("tab", "list", "--workspace", state["workspace_id"])["tabs"]
    tab = next((tab for tab in tabs if tab["tab_id"] == state["tab_id"]), None)
    if tab is None:
        # The environment is gone; only an exactly recorded route is safe to remove.
        removed = remove_owned_serve_route(state)
        state_path.unlink()
        print("real-device-stop: tab already closed; cleared stale record"
              + ("; tailnet route removed" if removed else ""))
        return
    panes = herdr("pane", "list", "--workspace", state["workspace_id"])["panes"]
    root = next((pane for pane in panes if pane["pane_id"] == state["root_pane_id"]), None)
    if (tab.get("label") != "real-device-validation" or root is None
            or root["tab_id"] != state["tab_id"] or root.get("cwd") != str(ROOT)):
        raise RuntimeError("real-device-stop: tab ownership check failed; nothing closed")
    # Refuse a changed route before closing the tab, so the operator can inspect it.
    removed = remove_owned_serve_route(state)
    herdr("tab", "close", state["tab_id"])
    state_path.unlink()
    print(f"real-device-stop: closed {state['tab_id']}; configs and data retained; shared server untouched"
          + ("; tailnet route removed" if removed else ""))


if __name__ == "__main__":
    try:
        stop()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

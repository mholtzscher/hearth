#!/usr/bin/env python3
"""Stop only the real-device validation tab recorded by this worktree."""

import json
import os
import subprocess
import sys

from start import ROOT, herdr


def stop():
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError("real-device-stop: run inside Herdr")
    state_path = ROOT / ".data/real-device-validation.json"
    if not state_path.exists():
        print("real-device-stop: no recorded environment; nothing closed")
        return
    state = json.loads(state_path.read_text())
    if state["worktree"] != str(ROOT):
        raise RuntimeError("real-device-stop: recorded worktree mismatch; nothing closed")
    tabs = herdr("tab", "list", "--workspace", state["workspace_id"])["tabs"]
    tab = next((tab for tab in tabs if tab["tab_id"] == state["tab_id"]), None)
    if tab is None:
        state_path.unlink()
        print("real-device-stop: tab already closed; cleared stale record")
        return
    panes = herdr("pane", "list", "--workspace", state["workspace_id"])["panes"]
    root = next((pane for pane in panes if pane["pane_id"] == state["root_pane_id"]), None)
    if (tab.get("label") != "real-device-validation" or root is None
            or root["tab_id"] != state["tab_id"] or root.get("cwd") != str(ROOT)):
        raise RuntimeError("real-device-stop: tab ownership check failed; nothing closed")
    herdr("tab", "close", state["tab_id"])
    state_path.unlink()
    print(f"real-device-stop: closed {state['tab_id']}; configs and data retained; shared server untouched")


if __name__ == "__main__":
    try:
        stop()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

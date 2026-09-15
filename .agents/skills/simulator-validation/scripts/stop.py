#!/usr/bin/env python3
"""Stop only the simulator validation tab recorded for this worktree.

Closes nothing it does not own: the tab label, root pane, recorded pane
membership, workspace, and Herdr session must all still match. Run data,
configs, and logs are always retained.
"""

import os
import subprocess
import sys

from start import ROOT, clear_state, herdr, lifecycle_lock, read_state, session_socket, state_path

FAULT = "simulator-stop"


def recorded_pane_ids(record):
    """Return every pane this run created, root first, without duplicates."""
    pane_ids = [pane_id for pane_id in (record.get("pane_ids") or []) if pane_id]
    root = record.get("root_pane_id")
    if root and root not in pane_ids:
        pane_ids.insert(0, root)
    return pane_ids


def verify_tab_ownership(record, tab, panes):
    """Refuse to close a tab whose label or recorded pane membership changed.

    A recorded pane that vanished, moved to another tab, or changed cwd is
    reported rather than closed, and an unexpected pane inside the owned tab
    blocks closure because closing the tab would destroy it too.
    """
    if tab.get("label") != record.get("tab_label"):
        raise RuntimeError(
            f"{FAULT}: tab {record['tab_id']} label {tab.get('label')!r} does not match recorded "
            f"{record.get('tab_label')!r}; nothing closed")
    recorded = recorded_pane_ids(record)
    by_id = {pane.get("pane_id"): pane for pane in panes}
    for pane_id in recorded:
        pane = by_id.get(pane_id)
        if pane is None:
            raise RuntimeError(
                f"{FAULT}: recorded pane {pane_id} is missing from workspace "
                f"{record['workspace_id']}; nothing closed")
        if pane.get("tab_id") != record["tab_id"]:
            raise RuntimeError(
                f"{FAULT}: recorded pane {pane_id} now belongs to {pane.get('tab_id')}; nothing closed")
        if pane.get("cwd") != record.get("worktree"):
            raise RuntimeError(
                f"{FAULT}: recorded pane {pane_id} cwd {pane.get('cwd')!r} does not match "
                f"{record.get('worktree')!r}; nothing closed")
    unexpected = [pane.get("pane_id") for pane in panes
                  if pane.get("tab_id") == record["tab_id"] and pane.get("pane_id") not in recorded]
    if unexpected:
        raise RuntimeError(
            f"{FAULT}: owned tab {record['tab_id']} contains unexpected panes {unexpected}; "
            "refusing to close")


def pane_still_exists(pane_id):
    """Report whether an opaque pane ID still resolves to a live pane."""
    try:
        herdr("pane", "get", pane_id)
        return True
    except (subprocess.CalledProcessError, ValueError, KeyError):
        return False


def stop():
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError(f"{FAULT}: run inside Herdr")
    with lifecycle_lock():
        return stop_owned_stack()


def stop_owned_stack():
    if os.environ.get("HERDR_ENV") != "1":
        raise RuntimeError(f"{FAULT}: run inside Herdr")
    if not state_path().exists():
        print(f"{FAULT}: no recorded environment; nothing closed")
        return
    record = read_state()
    if record.get("worktree") != str(ROOT):
        raise RuntimeError(f"{FAULT}: recorded worktree mismatch; nothing closed")
    recorded_socket = record.get("session_socket")
    current_socket = session_socket()
    if recorded_socket and current_socket and recorded_socket != current_socket:
        raise RuntimeError(
            f"{FAULT}: recorded Herdr session {recorded_socket} does not match current session "
            f"{current_socket}; nothing closed")
    if not record.get("tab_id"):
        # A start that reserved ownership but failed before creating its tab.
        clear_state()
        print(f"{FAULT}: released reserved start state; nothing closed")
        return

    workspace = record["workspace_id"]
    tab = next((candidate for candidate in herdr("tab", "list", "--workspace", workspace)["tabs"]
                if candidate.get("tab_id") == record["tab_id"]), None)
    if tab is None:
        alive = [pane_id for pane_id in recorded_pane_ids(record) if pane_still_exists(pane_id)]
        if alive:
            raise RuntimeError(
                f"{FAULT}: recorded tab {record['tab_id']} is closed but panes {alive} still exist; "
                "nothing cleared")
        clear_state()
        print(f"{FAULT}: tab {record['tab_id']} already closed; cleared stale record")
        return

    panes = herdr("pane", "list", "--workspace", workspace)["panes"]
    verify_tab_ownership(record, tab, panes)
    herdr("tab", "close", record["tab_id"])
    remaining = herdr("tab", "list", "--workspace", workspace)["tabs"]
    if any(candidate.get("tab_id") == record["tab_id"] for candidate in remaining):
        raise RuntimeError(
            f"{FAULT}: tab {record['tab_id']} closure not confirmed; record retained")
    clear_state()
    print(f"{FAULT}: closed {record['tab_id']}; run_dir {record.get('run_dir')} and logs retained")


if __name__ == "__main__":
    try:
        stop()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))

import { SettingsIcon } from "lucide-react";
import { useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";

import { api, ApiError, getBaseUrl, useBaseUrlVersion } from "../../api/client";
import { usePolling } from "../../api/hooks";
import { Button } from "../../components/button";
import { Input } from "../../components/input";
import { Label } from "../../components/label";
import { NativeSelect } from "../../components/native-select";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "../../components/popover";
import { StatusChip } from "../../components/status-chip";
import { navLinkClass } from "./sidebar";
import { useWorkspace } from "./workspace";

const NAV_ITEMS = [
  { to: "/devices", label: "Devices" },
  { to: "/entities", label: "Entities" },
  { to: "/adapters", label: "Adapters" },
  { to: "/automations", label: "Automations" },
  { to: "/commands", label: "Commands" },
  { to: "/notification-destinations", label: "Destinations" },
  { to: "/notifications", label: "Notifications" },
  { to: "/nats", label: "NATS" },
  { to: "/device-facts", label: "Device facts" },
];

/** Preserve an expected 503 readiness status; only transport/parse failures are unreachable. */
const readyStatus = (
  dataStatus: string | undefined,
  error: Error | null
): string => {
  if (dataStatus) {
    return dataStatus;
  }
  if (
    error instanceof ApiError &&
    error.status === 503 &&
    typeof error.detail === "object"
  ) {
    const { status } = error.detail as { status?: unknown };
    if (typeof status === "string") {
      return status;
    }
  }
  if (error) {
    return "unreachable";
  }
  return "unknown";
};

const HealthBadges = () => {
  const health = usePolling("healthz", api.health, 10_000);
  const ready = usePolling("readyz", api.ready, 10_000);
  return (
    <div className="flex items-center gap-1.5">
      <StatusChip
        label="healthz"
        status={readyStatus(health.data?.status, health.error)}
      />
      <StatusChip
        label="readyz"
        status={readyStatus(ready.data?.status, ready.error)}
      />
    </div>
  );
};

const EntityJump = () => {
  const [value, setValue] = useState("");
  const navigate = useNavigate();
  return (
    <Input
      aria-label="Jump to entity id"
      placeholder="Jump to entity id…"
      className="w-40 max-w-full"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && value.trim()) {
          navigate(`/entities/${value.trim()}`);
        }
      }}
    />
  );
};

/** Base URL and the OpenAPI link are set-once controls, so they live behind a
    popover instead of costing a toolbar row on every page. */
const ConnectionSettings = () => {
  const version = useBaseUrlVersion();
  const [baseUrl, setBaseUrl] = useState(getBaseUrl);
  const [lastVersion, setLastVersion] = useState(version);
  const { changeBackend } = useWorkspace();
  if (lastVersion !== version) {
    setLastVersion(version);
    setBaseUrl(getBaseUrl());
  }
  // Commit on blur/Enter so mid-typing values don't refetch every page.
  const commitBaseUrl = (value: string) => {
    changeBackend(value);
    setBaseUrl(getBaseUrl());
  };
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="Connection settings">
          <SettingsIcon />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <div className="grid gap-1.5">
          <Label htmlFor="base-url">hearthd base URL</Label>
          <Input
            id="base-url"
            placeholder="http://127.0.0.1:8080"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            onBlur={(e) => commitBaseUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                commitBaseUrl((e.target as HTMLInputElement).value);
              }
            }}
          />
          <p className="text-muted-foreground text-label">
            Empty uses the same origin (the vite proxy).
          </p>
        </div>
        <Button variant="outline" size="default" asChild>
          <a
            href={`${baseUrl || ""}/openapi.json`}
            target="_blank"
            rel="noreferrer"
          >
            Open OpenAPI JSON
          </a>
        </Button>
        <HealthBadges />
      </PopoverContent>
    </Popover>
  );
};

export const DevelopmentShell = () => {
  const location = useLocation();
  const navigate = useNavigate();
  const currentPage = NAV_ITEMS.find(
    (item) =>
      location.pathname === item.to ||
      location.pathname.startsWith(`${item.to}/`)
  );
  return (
    <div className="flex-1 overflow-auto">
      <header className="bg-background sticky top-0 z-40 border-b">
        <div className="max-tablet:px-5 flex flex-wrap items-center gap-2 px-7 py-2">
          <nav
            aria-label="Hearth Debug"
            className="max-desktop:hidden flex flex-wrap items-center gap-1"
          >
            {NAV_ITEMS.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                className={navLinkClass("full")}
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
          <div className="desktop:hidden min-w-32 flex-1">
            <NativeSelect
              aria-label="Development page"
              value={location.pathname}
              size="touch"
              onChange={(event) => navigate(event.target.value)}
            >
              {currentPage && location.pathname !== currentPage.to && (
                <option value={location.pathname} disabled>
                  {currentPage.label} detail
                </option>
              )}
              {NAV_ITEMS.map((item) => (
                <option key={item.to} value={item.to}>
                  {item.label}
                </option>
              ))}
            </NativeSelect>
          </div>
          <div className="ms-auto flex max-w-full min-w-0 items-center gap-2">
            <EntityJump />
            <ConnectionSettings />
          </div>
        </div>
      </header>
      <div className="max-tablet:px-5 max-tablet:pt-4 mx-auto max-w-[1440px] px-7 pt-[22px] pb-10">
        <Outlet />
      </div>
    </div>
  );
};

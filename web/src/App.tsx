import { cn } from "cn";
import { SettingsIcon } from "lucide-react";
import { useState } from "react";
import { createHashRouter, Navigate, NavLink, Outlet, RouterProvider, useLocation, useNavigate } from "react-router-dom";
import { api, ApiError, getBaseUrl, setBaseUrl } from "./api/client.ts";
import { usePolling } from "./api/hooks.ts";
import { StatusChip } from "./components/common.tsx";
import { Button } from "./components/ui/button.tsx";
import { Input } from "./components/ui/input.tsx";
import { Label } from "./components/ui/label.tsx";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/ui/popover.tsx";
import AdaptersPage from "./pages/AdaptersPage.tsx";
import AutomationDetailPage from "./pages/AutomationDetailPage.tsx";
import AutomationsPage from "./pages/AutomationsPage.tsx";
import AgentPage from "./pages/AgentPage.tsx";
import CommandsPage from "./pages/CommandsPage.tsx";
import DevicesPage from "./pages/DevicesPage.tsx";
import DeviceFactsPage from "./pages/DeviceFactsPage.tsx";
import EntitiesPage from "./pages/EntitiesPage.tsx";
import EntityDetailPage from "./pages/EntityDetailPage.tsx";
import NatsPage from "./pages/NatsPage.tsx";
import NotificationDestinationsPage from "./pages/NotificationDestinationsPage.tsx";
import NotificationDestinationDetailPage from "./pages/NotificationDestinationDetailPage.tsx";
import NotificationsPage from "./pages/NotificationsPage.tsx";
import { WorkspaceProvider } from "./features/dashboards/Workspace.tsx";
import { DashboardLibrary } from "./features/dashboards/DashboardLibrary.tsx";
import { Canvas } from "./features/dashboards/Canvas.tsx";
import { ViewDataProvider } from "./features/views/data/provider.tsx";

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

function HealthBadges() {
  const health = usePolling("healthz", api.health, 10_000);
  const ready = usePolling("readyz", api.ready, 10_000);
  return (
    <div className="flex items-center gap-1.5">
      <StatusChip label="healthz" status={readyStatus(health.data?.status, health.error)} />
      <StatusChip label="readyz" status={readyStatus(ready.data?.status, ready.error)} />
    </div>
  );
}

/** Preserve an expected 503 readiness status; only transport/parse failures are unreachable. */
function readyStatus(dataStatus: string | undefined, error: Error | null): string {
  if (dataStatus) return dataStatus;
  if (error instanceof ApiError && error.status === 503 && typeof error.detail === "object") {
    const status = (error.detail as { status?: unknown }).status;
    if (typeof status === "string") return status;
  }
  if (error) return "unreachable";
  return "unknown";
}

function EntityJump() {
  const [value, setValue] = useState("");
  const navigate = useNavigate();
  return (
    <Input
      aria-label="Jump to entity id"
      placeholder="Jump to entity id…"
      className="w-56 max-w-full shrink"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && value.trim()) navigate(`/entities/${value.trim()}`);
      }}
    />
  );
}

/** Base URL and the OpenAPI link are set-once controls, so they live behind a
    popover instead of costing a toolbar row on every page. */
function ConnectionSettings() {
  const [baseUrl, setBaseUrlState] = useState(getBaseUrl());
  // Commit on blur/Enter so mid-typing values don't refetch every page.
  function commitBaseUrl(value: string) {
    setBaseUrlState(value);
    setBaseUrl(value.trim());
  }
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="Connection settings">
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
            onChange={(e) => setBaseUrlState(e.target.value)}
            onBlur={(e) => commitBaseUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") commitBaseUrl((e.target as HTMLInputElement).value);
            }}
          />
          <p className="text-xs text-muted-foreground">
            Empty uses the same origin (the vite proxy).
          </p>
        </div>
        <Button variant="outline" size="sm" asChild>
          <a href={`${baseUrl || ""}/openapi.json`} target="_blank" rel="noreferrer">
            Open OpenAPI JSON
          </a>
        </Button>
      </PopoverContent>
    </Popover>
  );
}

function Shell() {
  const location = useLocation();
  const development = !["/", "/dashboards", "/agent"].includes(location.pathname) && !location.pathname.startsWith("/conversations/") && !location.pathname.startsWith("/dashboards/");
  return (
    <ViewDataProvider><WorkspaceProvider><div className="hearth-app">
      <aside className="hearth-sidebar" aria-label="Hearth navigation">
        <div className="hearth-brand"><span>⌂</span> hearth<small>A little closer to home.</small></div>
        <nav className="hearth-main-nav" aria-label="Main navigation">
          <NavLink to="/" end>⌂ <span>Home</span></NavLink>
          <NavLink to="/dashboards">▦ <span>Dashboards</span></NavLink>
          <NavLink to="/devices">⌘ <span>Development</span></NavLink>
        </nav>
        <p className="hearth-sidebar-foot">Your home, connected</p>
      </aside>
      <main className="hearth-main"><header className="hearth-topbar"><span>Your home <b>/</b> {development ? "Development" : location.pathname.startsWith("/dashboards") ? "Dashboards" : "Conversation"}</span><ConnectionSettings /></header>
      {development ? <DevelopmentShell /> : <Outlet />}
      </main>
    </div></WorkspaceProvider></ViewDataProvider>
  );
}

function DevelopmentShell() {
  return <div className="development-workspace">
      <header className="sticky top-0 z-40 border-b bg-background">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
          <span className="font-semibold">Hearth Debug</span>
          <nav className="flex flex-wrap items-center gap-0.5">
            {NAV_ITEMS.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                className={({ isActive }) =>
                  cn(
                    "rounded-lg px-2.5 py-1 text-sm transition-colors",
                    isActive
                      ? "bg-secondary font-medium text-secondary-foreground"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )
                }
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
          {/* max-w-full + wrap keeps this cluster inside a narrow viewport
              instead of pushing the page wider; it is unchanged when the
              header row has room. */}
          <div className="ms-auto flex min-w-0 max-w-full flex-wrap items-center gap-2">
            <EntityJump />
            <HealthBadges />
            <ConnectionSettings />
          </div>
        </div>
      </header>
      <main className="px-4 py-4">
        <Outlet />
      </main>
    </div>;
}

const router = createHashRouter([{ path: "/", element: <Shell />, children: [
  { index: true, element: <AgentPage /> },
  { path: "agent", element: <Navigate to="/" replace /> },
  { path: "conversations/:id", element: <AgentPage /> },
  { path: "dashboards", element: <DashboardLibrary /> },
  { path: "dashboards/:id", element: <Canvas dashboardPage /> },
  { path: "entities", element: <EntitiesPage /> },
  { path: "entities/:entityId", element: <EntityDetailPage /> },
  { path: "devices", element: <DevicesPage /> },
  { path: "adapters", element: <AdaptersPage /> },
  { path: "automations", element: <AutomationsPage /> },
  { path: "automations/:automationId", element: <AutomationDetailPage /> },
  { path: "commands", element: <CommandsPage /> },
  { path: "notification-destinations", element: <NotificationDestinationsPage /> },
  { path: "notification-destinations/:destinationId", element: <NotificationDestinationDetailPage /> },
  { path: "notifications", element: <NotificationsPage /> },
  { path: "notifications/:attemptId", element: <NotificationsPage /> },
  { path: "nats", element: <NatsPage /> },
  { path: "device-facts", element: <DeviceFactsPage /> },
] }]);
export default function App() {
  return <RouterProvider router={router} />;
}

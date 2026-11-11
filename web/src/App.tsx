import { cn } from "cn";
import { HistoryIcon, HouseIcon, LayoutDashboardIcon, MenuIcon, PlusIcon, SettingsIcon, WrenchIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { flushSync } from "react-dom";
import { createHashRouter, Link, Navigate, NavLink, Outlet, RouterProvider, useLocation, useMatch, useNavigate } from "react-router-dom";
import { api, ApiError, getBaseUrl, useBaseUrlVersion } from "./api/client.ts";
import { usePolling } from "./api/hooks.ts";
import { StatusChip } from "./components/common.tsx";
import { PhoneBarSlot } from "./components/PhoneBarSlot.ts";
import { ServerStatus, serverLabels, type ServerState } from "./components/ServerStatus.tsx";
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
import AutomationDraftPage from "./pages/AutomationDraftPage.tsx";
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
import { WorkspaceProvider, useWorkspace } from "./features/dashboards/Workspace.tsx";
import { DashboardLibrary } from "./features/dashboards/DashboardLibrary.tsx";
import { Canvas } from "./features/dashboards/Canvas.tsx";
import { ViewDataProvider } from "./features/views/data/provider.tsx";
import { ConversationHistory } from "./features/conversation/ConversationHistory.tsx";
import { ConversationsProvider, useConversations } from "./features/conversation/ConversationsProvider.tsx";

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
      className="w-40 max-w-full shrink transition-[width] focus:w-56 motion-reduce:transition-none"
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
  const version = useBaseUrlVersion();
  const { changeBackend } = useWorkspace();
  useEffect(() => setBaseUrlState(getBaseUrl()), [version]);
  // Commit on blur/Enter so mid-typing values don't refetch every page.
  function commitBaseUrl(value: string) {
    changeBackend(value);
    setBaseUrlState(getBaseUrl());
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
  return <ViewDataProvider><ConversationsProvider><div className="hearth-app"><WorkspaceProvider><HouseholdShell /></WorkspaceProvider></div></ConversationsProvider></ViewDataProvider>;
}

/**
 * The sidebar's form: a drawer behind a top bar on phones, a 104px rail up to 980px, and full
 * width above that, except beside a canvas below 1280px, where the rail leaves
 * the view room for its cards.
 */
function useNavMode(canvasOpen: boolean): "full" | "rail" | "bar" {
  const phone = useMediaQuery("(max-width: 720px)");
  const narrow = useMediaQuery("(max-width: 980px)");
  const crowded = useMediaQuery("(max-width: 1279px)");
  if (phone) return "bar";
  return narrow || (canvasOpen && crowded) ? "rail" : "full";
}

function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (change) => {
      const media = window.matchMedia?.(query);
      media?.addEventListener("change", change);
      return () => media?.removeEventListener("change", change);
    },
    () => window.matchMedia?.(query).matches ?? false,
  );
}

function HouseholdShell() {
  const location = useLocation();
  const workspace = useWorkspace();
  const { startNew } = useConversations();
  const conversationId = useMatch("/conversations/:id")?.params.id;
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [server, setServer] = useState<ServerState>("checking");
  const [barSlot, setBarSlot] = useState<HTMLElement | null>(null);
  const historyButton = useRef<HTMLButtonElement>(null);
  const menuButton = useRef<HTMLButtonElement>(null);
  const development = !["/", "/dashboards", "/agent"].includes(location.pathname) && !location.pathname.startsWith("/conversations/") && !location.pathname.startsWith("/dashboards/");
  const home = location.pathname === "/" || !!conversationId;
  const nav = useNavMode(home && !!workspace.canvasDraft);
  // The rail opens Recent as a drawer; on phones the whole sidebar is the drawer.
  const toggle = nav === "bar" ? menuButton : historyButton;
  const closeDrawer = useCallback(() => {
    // The phone bar is inert while the drawer is open; release it before returning focus.
    flushSync(() => setDrawerOpen(false));
    toggle.current?.focus();
  }, [toggle]);
  useEffect(() => setDrawerOpen(false), [location.pathname, nav]);
  useEffect(() => {
    if (!drawerOpen) return;
    // The drawer follows the sidebar or bar in DOM order, so move focus into it explicitly.
    if (nav === "bar") document.getElementById("hearth-drawer-close")?.focus();
    else {
      const history = document.getElementById("conversation-history");
      (history?.querySelector<HTMLElement>("a[aria-current=page], a") ?? history?.querySelector<HTMLElement>("h2"))?.focus();
    }
    function keydown(event: KeyboardEvent) { if (event.key === "Escape") closeDrawer(); }
    document.addEventListener("keydown", keydown);
    return () => document.removeEventListener("keydown", keydown);
  }, [drawerOpen, nav, closeDrawer]);
  function newConversation() {
    setDrawerOpen(false);
    workspace.startFresh(() => { startNew(); workspace.navigateRetainingDraft("/"); });
  }
  const serverProblem = server === "not-ready" || server === "unavailable";
  return <PhoneBarSlot.Provider value={nav === "bar" ? barSlot : null}>
    {nav === "bar" && <header className="phone-bar" inert={drawerOpen}>
      <button ref={menuButton} className="icon-button phone-menu" data-server={serverProblem ? server : undefined} aria-label={serverProblem ? `Open navigation, Hearth server ${serverLabels[server]}` : "Open navigation"} aria-expanded={drawerOpen} aria-controls="hearth-sidebar" onClick={() => setDrawerOpen(true)}><MenuIcon aria-hidden="true" /></button>
      <div className="phone-bar-center">
        <span className="phone-bar-title">{home ? "Home" : development ? "Development" : "Dashboards"}</span>
        <div ref={setBarSlot} className="phone-bar-slot" />
      </div>
      <button className="icon-button phone-new" aria-label="New conversation" onClick={newConversation}><PlusIcon aria-hidden="true" /></button>
    </header>}
    <aside id="hearth-sidebar" className={`hearth-sidebar${nav === "bar" && drawerOpen ? " open" : ""}`} data-nav={nav} aria-label="Hearth navigation">
      <div className="hearth-brand"><span><HouseIcon aria-hidden="true" /></span> hearth<small>A little closer to home.</small></div>
      {nav === "bar" && <button id="hearth-drawer-close" className="icon-button drawer-close" aria-label="Close navigation" onClick={closeDrawer}><XIcon aria-hidden="true" /></button>}
      <button className="sidebar-new" aria-label="New conversation" onClick={newConversation}><PlusIcon aria-hidden="true" /><span>New conversation</span></button>
      {/* Choosing the page already open changes no route, so close the phone drawer on any link. */}
      <nav className="hearth-main-nav" aria-label="Main navigation" onClick={event => { if ((event.target as Element).closest("a")) setDrawerOpen(false); }}>
        <Link to="/" aria-label="Home" className={home ? "active" : undefined} aria-current={home ? "page" : undefined}><HouseIcon aria-hidden="true" /><span>Home</span></Link>
        <NavLink to="/dashboards" aria-label="Dashboards"><LayoutDashboardIcon aria-hidden="true" /><span>Dashboards</span></NavLink>
        <NavLink to="/devices" aria-label="Development"><WrenchIcon aria-hidden="true" /><span>Development</span></NavLink>
      </nav>
      <button ref={historyButton} className="history-toggle" aria-label="Conversation history" aria-expanded={nav === "rail" && drawerOpen} aria-controls="conversation-history" onClick={() => setDrawerOpen(open => !open)}><HistoryIcon aria-hidden="true" /><span>Recent</span></button>
      <ConversationHistory id="conversation-history" open={nav === "rail" && drawerOpen} onNavigate={() => setDrawerOpen(false)} onClose={nav === "rail" ? closeDrawer : undefined} />
      <ServerStatus onChange={setServer} />
    </aside>
    {drawerOpen && <div className="drawer-backdrop" aria-hidden="true" onClick={() => setDrawerOpen(false)} />}
    <main className="hearth-main" inert={drawerOpen}>{development ? <DevelopmentShell /> : <Outlet />}</main>
  </PhoneBarSlot.Provider>;
}

function DevelopmentShell() {
  return <div className="development-workspace">
      <header className="sticky top-0 z-40 border-b bg-background">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
          {/* The sidebar already names this area, so the page list needs no visible title. */}
          <nav aria-label="Hearth Debug" className="flex flex-wrap items-center gap-0.5">
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
  { path: "automations/new", element: <AutomationDraftPage /> },
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

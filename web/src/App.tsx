import { cn } from "./lib/utils.ts";
import { HistoryIcon, HouseIcon, LayoutDashboardIcon, MenuIcon, PlusIcon, SettingsIcon, WrenchIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { flushSync } from "react-dom";
import { Link, Navigate, NavLink, Outlet, RouterProvider, useLocation, useMatch, useNavigate, type RouteObject, type RouterProviderProps } from "react-router-dom";
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
import { useVisibleViewport } from "./lib/visibleViewport.ts";

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
        <Button variant="outline" size="default" asChild>
          <a href={`${baseUrl || ""}/openapi.json`} target="_blank" rel="noreferrer">
            Open OpenAPI JSON
          </a>
        </Button>
      </PopoverContent>
    </Popover>
  );
}

function Shell() {
  useVisibleViewport();
  return <ViewDataProvider><ConversationsProvider><WorkspaceProvider><HouseholdShell /></WorkspaceProvider></ConversationsProvider></ViewDataProvider>;
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
  const navLink = cn(
    "group flex min-h-11 items-center gap-3 rounded-md p-[11px] text-muted-foreground no-underline hover:bg-gray-2 hover:text-foreground aria-[current=page]:bg-gray-2 aria-[current=page]:text-foreground",
    nav === "rail" && "flex-col justify-center gap-1.5 px-1 py-3 text-caption",
    nav === "bar" && "hover:bg-gray-3 aria-[current=page]:bg-gray-3",
  );
  const navIcon = "size-[18px] shrink-0 group-aria-[current=page]:text-violet";
  const hideWhileSearching = nav === "bar" ? "group-has-[[data-slot=history-search]:focus-within]/sidebar:hidden group-has-[[data-slot=history-search]_input:not(:placeholder-shown)]/sidebar:hidden" : undefined;
  return <PhoneBarSlot.Provider value={nav === "bar" ? barSlot : null}>
    <div className={cn("grid h-[var(--visible-height,100dvh)] grid-cols-[clamp(218px,22vw,340px)_minmax(0,1fr)] bg-background text-label text-foreground [translate:0_var(--visible-top,0px)]", nav === "rail" && "grid-cols-[104px_minmax(0,1fr)]", nav === "bar" && "grid-cols-[minmax(0,1fr)] grid-rows-[52px_minmax(0,1fr)]")}>
    {nav === "bar" && <header className="grid grid-cols-[44px_minmax(0,1fr)_44px] items-center gap-2 border-b border-border bg-background px-2 py-1" inert={drawerOpen}>
      <Button ref={menuButton} variant="ghost" size="icon-touch" className={cn("relative aria-expanded:bg-transparent", serverProblem && "after:absolute after:top-[10px] after:right-[9px] after:size-2 after:rounded-full after:bg-warning after:shadow-[0_0_0_2px_var(--background)] after:content-['']", server === "unavailable" && "after:bg-destructive")} aria-label={serverProblem ? `Open navigation, Hearth server ${serverLabels[server]}` : "Open navigation"} aria-expanded={drawerOpen} aria-controls="hearth-sidebar" onClick={() => setDrawerOpen(true)}><MenuIcon aria-hidden="true" /></Button>
      <div className="group/phone-bar flex min-w-0 justify-center">
        <span data-slot="phone-bar-title" className="truncate text-body font-semibold tracking-[-.01em] text-foreground group-has-[[data-slot=phone-bar-slot]:not(:empty)]/phone-bar:hidden">{home ? "Home" : development ? "Development" : "Dashboards"}</span>
        <div ref={setBarSlot} data-slot="phone-bar-slot" />
      </div>
      <Button variant="ghost" size="icon-touch" aria-label="New conversation" onClick={newConversation}><PlusIcon className="text-violet" aria-hidden="true" /></Button>
    </header>}
    <aside id="hearth-sidebar" className={cn("group/sidebar flex min-h-0 flex-col gap-6 border-r border-border bg-background px-4 pt-7 pb-4", nav === "rail" && "gap-4 px-1.5 pt-6", nav === "bar" && "invisible fixed inset-y-0 left-0 z-[60] w-[min(320px,85vw)] -translate-x-3 gap-4 bg-card px-3 pt-4 pb-[max(16px,var(--safe-bottom,env(safe-area-inset-bottom)))] opacity-0 shadow-[12px_0_40px_#0006] [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_.18s]", nav === "bar" && drawerOpen && "visible translate-x-0 opacity-100 [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_0s]")} data-nav={nav} aria-label="Hearth navigation">
      <div data-slot="sidebar-brand" className={cn("px-2 text-2xl leading-normal font-semibold tracking-[-.03em]", nav === "rail" && "text-center text-[0px]", nav === "bar" && "min-h-11 pt-2 pe-[52px] ps-3 text-xl", hideWhileSearching)}><span className="inline-flex align-middle text-violet"><HouseIcon className={cn("size-6", nav === "bar" && "size-5")} aria-hidden="true" /></span> hearth<small className={cn("mt-1.5 block text-label leading-normal font-normal tracking-normal text-muted-foreground", nav !== "full" && "hidden")}>A little closer to home.</small></div>
      {nav === "bar" && <Button variant="ghost" size="icon-touch" id="hearth-drawer-close" className="absolute top-4 right-2" aria-label="Close navigation" onClick={closeDrawer}><XIcon aria-hidden="true" /></Button>}
      <Button variant="outline" size="touch" className={cn("justify-start gap-2.5 font-medium", nav === "rail" && "justify-center px-0", hideWhileSearching)} aria-label="New conversation" onClick={newConversation}><PlusIcon className="size-[18px] text-violet" aria-hidden="true" /><span className={nav === "rail" ? "hidden" : undefined}>New conversation</span></Button>
      {/* Choosing the page already open changes no route, so close the phone drawer on any link. */}
      <nav className={cn("grid gap-1", nav === "bar" && "gap-0.5", hideWhileSearching)} aria-label="Main navigation" onClick={event => { if ((event.target as Element).closest("a")) setDrawerOpen(false); }}>
        <Link to="/" aria-label="Home" className={navLink} aria-current={home ? "page" : undefined}><HouseIcon className={navIcon} aria-hidden="true" /><span>Home</span></Link>
        <NavLink to="/dashboards" aria-label="Dashboards" className={navLink}><LayoutDashboardIcon className={navIcon} aria-hidden="true" /><span>Dashboards</span></NavLink>
        <NavLink to="/devices" aria-label="Development" className={navLink}><WrenchIcon className={navIcon} aria-hidden="true" /><span>Development</span></NavLink>
      </nav>
      {nav === "rail" && <div className={cn("rounded-md py-[11px] hover:bg-gray-2", drawerOpen && "bg-gray-2")}><Button ref={historyButton} variant="ghost" size="touch" className="w-full flex-col gap-1.5 px-1 py-0 text-caption hover:bg-transparent aria-expanded:bg-transparent" aria-label="Recent conversation history" aria-expanded={drawerOpen} aria-controls="conversation-history" onClick={() => setDrawerOpen(open => !open)}><HistoryIcon className="size-[18px]" aria-hidden="true" /><span>Recent</span></Button></div>}
      <ConversationHistory id="conversation-history" open={nav === "rail" && drawerOpen} onNavigate={() => setDrawerOpen(false)} onClose={nav === "rail" ? closeDrawer : undefined} classNames={{
        root: nav === "rail" ? cn("invisible fixed inset-y-0 left-[104px] z-[60] w-[min(320px,85vw)] -translate-x-3 border-t-0 border-r border-border bg-card px-3 pt-3 pb-4 opacity-0 shadow-[12px_0_40px_#0006] [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_.18s] [--history-bg:var(--gray-1)]", drawerOpen && "visible translate-x-0 opacity-100 [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_0s]")
          : nav === "bar" ? "pt-4 [--history-bg:var(--gray-1)] has-[input:focus]:border-t-0 has-[input:focus]:pt-3 has-[input:not(:placeholder-shown)]:border-t-0 has-[input:not(:placeholder-shown)]:pt-3" : undefined,
        header: nav === "rail" ? "pt-0 pe-0 pb-1 ps-3" : undefined,
        close: nav === "rail" ? "-me-1" : undefined,
      }} />
      <ServerStatus onChange={setServer} rail={nav === "rail"} className={hideWhileSearching} />
    </aside>
    {drawerOpen && <div className="fixed inset-0 z-[55] bg-background/[.533333]" aria-hidden="true" onClick={() => setDrawerOpen(false)} />}
    <main className="flex min-h-0 min-w-0 flex-col overflow-hidden" inert={drawerOpen}>{development ? <DevelopmentShell /> : <Outlet />}</main>
    </div>
  </PhoneBarSlot.Provider>;
}

function DevelopmentShell() {
  return <div className="flex-1 overflow-auto">
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
      <div className="px-4 py-4">
        <Outlet />
      </div>
    </div>;
}

export const appRoutes: RouteObject[] = [{ path: "/", element: <Shell />, children: [
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
] }];

type AppProps = {
  /** The caller creates and disposes this router. The browser uses hash routing; embedded previews use memory routing. */
  router: RouterProviderProps["router"];
};

export default function App({ router }: AppProps) {
  return <RouterProvider router={router} />;
}

import {
  HistoryIcon,
  HouseIcon,
  LayoutDashboardIcon,
  MenuIcon,
  PlusIcon,
  SettingsIcon,
  WrenchIcon,
  XIcon,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { RefObject } from "react";
import { flushSync } from "react-dom";
import {
  Link,
  Navigate,
  NavLink,
  Outlet,
  RouterProvider,
  useLocation,
  useMatch,
  useNavigate,
} from "react-router-dom";
import type { RouteObject, RouterProviderProps } from "react-router-dom";

import { api, ApiError, getBaseUrl, useBaseUrlVersion } from "./api/client.ts";
import { usePolling } from "./api/hooks.ts";
import { Button } from "./components/button.tsx";
import { Input } from "./components/input.tsx";
import { Label } from "./components/label.tsx";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/popover.tsx";
import { StatusChip } from "./components/status-chip.tsx";
import { ConversationHistory } from "./features/conversation/conversation-history.tsx";
import {
  ConversationsProvider,
  useConversations,
} from "./features/conversation/conversations-provider.tsx";
import { Canvas } from "./features/dashboards/canvas.tsx";
import { DashboardLibrary } from "./features/dashboards/dashboard-library.tsx";
import {
  WorkspaceProvider,
  useWorkspace,
} from "./features/dashboards/workspace.tsx";
import { ViewDataProvider } from "./features/views/data/provider.tsx";
import { PhoneBarSlot } from "./features/workspace/phone-bar-slot.ts";
import {
  ServerStatus,
  serverLabels,
} from "./features/workspace/server-status.tsx";
import type { ServerState } from "./features/workspace/server-status.tsx";
import { cn } from "./lib/utils.ts";
import { useVisibleViewport } from "./lib/visible-viewport.ts";
import AdaptersPage from "./pages/adapters-page.tsx";
import AgentPage from "./pages/agent-page.tsx";
import AutomationDetailPage from "./pages/automation-detail-page.tsx";
import AutomationDraftPage from "./pages/automation-draft-page.tsx";
import AutomationsPage from "./pages/automations-page.tsx";
import CommandsPage from "./pages/commands-page.tsx";
import DeviceFactsPage from "./pages/device-facts-page.tsx";
import DevicesPage from "./pages/devices-page.tsx";
import EntitiesPage from "./pages/entities-page.tsx";
import EntityDetailPage from "./pages/entity-detail-page.tsx";
import NatsPage from "./pages/nats-page.tsx";
import NotificationDestinationDetailPage from "./pages/notification-destination-detail-page.tsx";
import NotificationDestinationsPage from "./pages/notification-destinations-page.tsx";
import NotificationsPage from "./pages/notifications-page.tsx";

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

type NavMode = "full" | "rail" | "bar";

const navIconClass =
  "size-[18px] shrink-0 group-aria-[current=page]:text-violet";

const navLinkClass = (nav: NavMode): string =>
  cn(
    "group text-muted-foreground hover:bg-gray-2 hover:text-foreground aria-[current=page]:bg-gray-2 aria-[current=page]:text-foreground flex min-h-11 items-center gap-3 rounded-md p-[11px] no-underline",
    nav === "rail" && "text-caption flex-col justify-center gap-1.5 px-1 py-3",
    nav === "bar" && "hover:bg-gray-3 aria-[current=page]:bg-gray-3"
  );

const hideWhileSearchingClass = (nav: NavMode): string | undefined =>
  nav === "bar"
    ? "group-has-[[data-slot=history-search]:focus-within]/sidebar:hidden group-has-[[data-slot=history-search]_input:not(:placeholder-shown)]/sidebar:hidden"
    : undefined;

const pageTitle = (home: boolean, development: boolean): string => {
  if (home) {
    return "Home";
  }
  if (development) {
    return "Development";
  }
  return "Dashboards";
};

const historyClassNames = (nav: NavMode, drawerOpen: boolean) => {
  let root: string | undefined;
  if (nav === "rail") {
    root = cn(
      "border-border bg-card invisible fixed inset-y-0 left-[104px] z-[60] w-[min(320px,85vw)] -translate-x-3 border-t-0 border-r px-3 pt-3 pb-4 opacity-0 shadow-[12px_0_40px_#0006] [--history-bg:var(--gray-1)] [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_.18s]",
      drawerOpen &&
        "visible translate-x-0 opacity-100 [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_0s]"
    );
  } else if (nav === "bar") {
    root =
      "pt-4 [--history-bg:var(--gray-1)] has-[input:focus]:border-t-0 has-[input:focus]:pt-3 has-[input:not(:placeholder-shown)]:border-t-0 has-[input:not(:placeholder-shown)]:pt-3";
  }
  return {
    root,
    header: nav === "rail" ? "pt-0 pe-0 pb-1 ps-3" : undefined,
    close: nav === "rail" ? "-me-1" : undefined,
  };
};

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
      className="w-40 max-w-full shrink transition-[width] focus:w-56 motion-reduce:transition-none"
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
          <p className="text-muted-foreground text-xs">
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
      </PopoverContent>
    </Popover>
  );
};

/**
 * The sidebar's form: a drawer behind a top bar on phones, a 104px rail up to 980px, and full
 * width above that, except beside a canvas below 1280px, where the rail leaves
 * the view room for its cards.
 */
const useNavMode = (canvasOpen: boolean): NavMode => {
  const phone = useMediaQuery("(max-width: 720px)");
  const narrow = useMediaQuery("(max-width: 980px)");
  const crowded = useMediaQuery("(max-width: 1279px)");
  if (phone) {
    return "bar";
  }
  return narrow || (canvasOpen && crowded) ? "rail" : "full";
};

const useMediaQuery = (query: string): boolean =>
  useSyncExternalStore(
    (change) => {
      const media = window.matchMedia?.(query);
      media?.addEventListener("change", change);
      return () => media?.removeEventListener("change", change);
    },
    () => window.matchMedia?.(query).matches ?? false
  );

const SidebarBrand = ({
  nav,
  hideWhileSearching,
}: {
  nav: NavMode;
  hideWhileSearching: string | undefined;
}) => (
  <div
    data-slot="sidebar-brand"
    className={cn(
      "px-2 text-2xl leading-normal font-semibold tracking-[-.03em]",
      nav === "rail" && "text-center text-[0px]",
      nav === "bar" && "min-h-11 ps-3 pe-[52px] pt-2 text-xl",
      hideWhileSearching
    )}
  >
    <span className="text-violet inline-flex align-middle">
      <HouseIcon
        className={cn("size-6", nav === "bar" && "size-5")}
        aria-hidden="true"
      />
    </span>{" "}
    hearth
    <small
      className={cn(
        "text-label text-muted-foreground mt-1.5 block leading-normal font-normal tracking-normal",
        nav !== "full" && "hidden"
      )}
    >
      A little closer to home.
    </small>
  </div>
);

const MainNavigation = ({
  nav,
  home,
  hideWhileSearching,
  onNavigate,
}: {
  nav: NavMode;
  home: boolean;
  hideWhileSearching: string | undefined;
  onNavigate: () => void;
}) => {
  const linkClass = navLinkClass(nav);
  return (
    <nav
      className={cn(
        "grid gap-1",
        nav === "bar" && "gap-0.5",
        hideWhileSearching
      )}
      aria-label="Main navigation"
    >
      <Link
        to="/"
        aria-label="Home"
        className={linkClass}
        aria-current={home ? "page" : undefined}
        onClick={onNavigate}
      >
        <HouseIcon className={navIconClass} aria-hidden="true" />
        <span>Home</span>
      </Link>
      <NavLink
        to="/dashboards"
        aria-label="Dashboards"
        className={linkClass}
        onClick={onNavigate}
      >
        <LayoutDashboardIcon className={navIconClass} aria-hidden="true" />
        <span>Dashboards</span>
      </NavLink>
      <NavLink
        to="/devices"
        aria-label="Development"
        className={linkClass}
        onClick={onNavigate}
      >
        <WrenchIcon className={navIconClass} aria-hidden="true" />
        <span>Development</span>
      </NavLink>
    </nav>
  );
};

const RecentNavigationButton = ({
  drawerOpen,
  historyButton,
  onToggle,
}: {
  drawerOpen: boolean;
  historyButton: RefObject<HTMLButtonElement | null>;
  onToggle: () => void;
}) => (
  <div
    className={cn(
      "hover:bg-gray-2 rounded-md py-[11px]",
      drawerOpen && "bg-gray-2"
    )}
  >
    <Button
      ref={historyButton}
      variant="ghost"
      size="touch"
      className="text-caption w-full flex-col gap-1.5 px-1 py-0 hover:bg-transparent aria-expanded:bg-transparent"
      aria-label="Recent conversation history"
      aria-expanded={drawerOpen}
      aria-controls="conversation-history"
      onClick={onToggle}
    >
      <HistoryIcon className="size-[18px]" aria-hidden="true" />
      <span>Recent</span>
    </Button>
  </div>
);

const PhoneNavigationBar = ({
  drawerOpen,
  server,
  home,
  development,
  menuButton,
  onNewConversation,
  onOpenDrawer,
  setBarSlot,
}: {
  drawerOpen: boolean;
  server: ServerState;
  home: boolean;
  development: boolean;
  menuButton: RefObject<HTMLButtonElement | null>;
  onNewConversation: () => void;
  onOpenDrawer: () => void;
  setBarSlot: (element: HTMLElement | null) => void;
}) => {
  const serverProblem = server === "not-ready" || server === "unavailable";
  return (
    <header
      className="border-border bg-background grid grid-cols-[44px_minmax(0,1fr)_44px] items-center gap-2 border-b px-2 py-1"
      inert={drawerOpen}
    >
      <Button
        ref={menuButton}
        variant="ghost"
        size="icon-touch"
        className={cn(
          "relative aria-expanded:bg-transparent",
          serverProblem &&
            "after:bg-warning after:absolute after:top-[10px] after:right-[9px] after:size-2 after:rounded-full after:shadow-[0_0_0_2px_var(--background)] after:content-['']",
          server === "unavailable" && "after:bg-destructive"
        )}
        aria-label={
          serverProblem
            ? `Open navigation, Hearth server ${serverLabels[server]}`
            : "Open navigation"
        }
        aria-expanded={drawerOpen}
        aria-controls="hearth-sidebar"
        onClick={onOpenDrawer}
      >
        <MenuIcon aria-hidden="true" />
      </Button>
      <div className="group/phone-bar flex min-w-0 justify-center">
        <span
          data-slot="phone-bar-title"
          className="text-body text-foreground truncate font-semibold tracking-[-.01em] group-has-[[data-slot=phone-bar-slot]:not(:empty)]/phone-bar:hidden"
        >
          {pageTitle(home, development)}
        </span>
        <div ref={setBarSlot} data-slot="phone-bar-slot" />
      </div>
      <Button
        variant="ghost"
        size="icon-touch"
        aria-label="New conversation"
        onClick={onNewConversation}
      >
        <PlusIcon className="text-violet" aria-hidden="true" />
      </Button>
    </header>
  );
};

const HouseholdSidebar = ({
  nav,
  drawerOpen,
  home,
  historyButton,
  onCloseDrawer,
  onNavigate,
  onNewConversation,
  onToggleRecent,
  setServer,
}: {
  nav: NavMode;
  drawerOpen: boolean;
  home: boolean;
  historyButton: RefObject<HTMLButtonElement | null>;
  onCloseDrawer: () => void;
  onNavigate: () => void;
  onNewConversation: () => void;
  onToggleRecent: () => void;
  setServer: (server: ServerState) => void;
}) => {
  const hideWhileSearching = hideWhileSearchingClass(nav);
  return (
    <aside
      id="hearth-sidebar"
      className={cn(
        "group/sidebar border-border bg-background flex min-h-0 flex-col gap-6 border-r px-4 pt-7 pb-4",
        nav === "rail" && "gap-4 px-1.5 pt-6",
        nav === "bar" &&
          "bg-card invisible fixed inset-y-0 left-0 z-[60] w-[min(320px,85vw)] -translate-x-3 gap-4 px-3 pt-4 pb-[max(16px,var(--safe-bottom,env(safe-area-inset-bottom)))] opacity-0 shadow-[12px_0_40px_#0006] [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_.18s]",
        nav === "bar" &&
          drawerOpen &&
          "visible translate-x-0 opacity-100 [transition:opacity_.18s_ease-out,transform_.18s_cubic-bezier(.16,1,.3,1),visibility_0s_linear_0s]"
      )}
      data-nav={nav}
      aria-label="Hearth navigation"
    >
      <SidebarBrand nav={nav} hideWhileSearching={hideWhileSearching} />
      {nav === "bar" && (
        <Button
          variant="ghost"
          size="icon-touch"
          id="hearth-drawer-close"
          className="absolute top-4 right-2"
          aria-label="Close navigation"
          onClick={onCloseDrawer}
        >
          <XIcon aria-hidden="true" />
        </Button>
      )}
      <Button
        variant="outline"
        size="touch"
        className={cn(
          "justify-start gap-2.5 font-medium",
          nav === "rail" && "justify-center px-0",
          hideWhileSearching
        )}
        aria-label="New conversation"
        onClick={onNewConversation}
      >
        <PlusIcon className="text-violet size-[18px]" aria-hidden="true" />
        <span className={nav === "rail" ? "hidden" : undefined}>
          New conversation
        </span>
      </Button>
      {/* Choosing the page already open changes no route, so close the phone drawer on any link. */}
      <MainNavigation
        nav={nav}
        home={home}
        hideWhileSearching={hideWhileSearching}
        onNavigate={onNavigate}
      />
      {nav === "rail" && (
        <RecentNavigationButton
          drawerOpen={drawerOpen}
          historyButton={historyButton}
          onToggle={onToggleRecent}
        />
      )}
      <ConversationHistory
        id="conversation-history"
        open={nav === "rail" && drawerOpen}
        onNavigate={onNavigate}
        onClose={nav === "rail" ? onCloseDrawer : undefined}
        classNames={historyClassNames(nav, drawerOpen)}
      />
      <ServerStatus
        onChange={setServer}
        rail={nav === "rail"}
        className={hideWhileSearching}
      />
    </aside>
  );
};

const DevelopmentShell = () => (
  <div className="flex-1 overflow-auto">
    <header className="bg-background sticky top-0 z-40 border-b">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
        {/* The sidebar already names this area, so the page list needs no visible title. */}
        <nav
          aria-label="Hearth Debug"
          className="flex flex-wrap items-center gap-0.5"
        >
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  "rounded-lg px-2.5 py-1 text-sm transition-colors",
                  isActive
                    ? "bg-secondary text-secondary-foreground font-medium"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
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
        <div className="ms-auto flex max-w-full min-w-0 flex-wrap items-center gap-2">
          <EntityJump />
          <HealthBadges />
          <ConnectionSettings />
        </div>
      </div>
    </header>
    <div className="px-4 py-4">
      <Outlet />
    </div>
  </div>
);

const HouseholdShell = () => {
  const location = useLocation();
  const workspace = useWorkspace();
  const { startNew } = useConversations();
  const conversationId = useMatch("/conversations/:id")?.params.id;
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [server, setServer] = useState<ServerState>("checking");
  const [barSlot, setBarSlot] = useState<HTMLElement | null>(null);
  const historyButton = useRef<HTMLButtonElement>(null);
  const menuButton = useRef<HTMLButtonElement>(null);
  const development =
    !["/", "/dashboards", "/agent"].includes(location.pathname) &&
    !location.pathname.startsWith("/conversations/") &&
    !location.pathname.startsWith("/dashboards/");
  const home = location.pathname === "/" || !!conversationId;
  const nav = useNavMode(home && !!workspace.canvasDraft);
  const routeKey = `${location.pathname}:${nav}`;
  const [lastRouteKey, setLastRouteKey] = useState(routeKey);
  if (lastRouteKey !== routeKey) {
    setLastRouteKey(routeKey);
    setDrawerOpen(false);
  }
  // The rail opens Recent as a drawer; on phones the whole sidebar is the drawer.
  const toggle = nav === "bar" ? menuButton : historyButton;
  const closeDrawer = useCallback(() => {
    // The phone bar is inert while the drawer is open; release it before returning focus.
    flushSync(() => setDrawerOpen(false));
    toggle.current?.focus();
  }, [toggle]);
  useEffect(() => {
    if (!drawerOpen) {
      return;
    }
    // The drawer follows the sidebar or bar in DOM order, so move focus into it explicitly.
    if (nav === "bar") {
      document.querySelector<HTMLElement>("#hearth-drawer-close")?.focus();
    } else {
      const history = document.querySelector("#conversation-history");
      (
        history?.querySelector<HTMLElement>("a[aria-current=page], a") ??
        history?.querySelector<HTMLElement>("h2")
      )?.focus();
    }
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeDrawer();
      }
    };
    document.addEventListener("keydown", keydown);
    return () => document.removeEventListener("keydown", keydown);
  }, [drawerOpen, nav, closeDrawer]);
  const newConversation = () => {
    setDrawerOpen(false);
    workspace.startFresh(() => {
      startNew();
      workspace.navigateRetainingDraft("/");
    });
  };
  const closePhoneDrawer = () => setDrawerOpen(false);
  return (
    <PhoneBarSlot.Provider value={nav === "bar" ? barSlot : null}>
      <div
        className={cn(
          "bg-background text-label text-foreground grid h-[var(--visible-height,100dvh)] [translate:0_var(--visible-top,0px)] grid-cols-[clamp(218px,22vw,340px)_minmax(0,1fr)]",
          nav === "rail" && "grid-cols-[104px_minmax(0,1fr)]",
          nav === "bar" &&
            "grid-cols-[minmax(0,1fr)] grid-rows-[52px_minmax(0,1fr)]"
        )}
      >
        {nav === "bar" && (
          <PhoneNavigationBar
            drawerOpen={drawerOpen}
            server={server}
            home={home}
            development={development}
            menuButton={menuButton}
            onNewConversation={newConversation}
            onOpenDrawer={() => setDrawerOpen(true)}
            setBarSlot={setBarSlot}
          />
        )}
        <HouseholdSidebar
          nav={nav}
          drawerOpen={drawerOpen}
          home={home}
          historyButton={historyButton}
          onCloseDrawer={closeDrawer}
          onNavigate={closePhoneDrawer}
          onNewConversation={newConversation}
          onToggleRecent={() => setDrawerOpen((open) => !open)}
          setServer={setServer}
        />
        {drawerOpen && (
          <div
            className="bg-background/[.533333] fixed inset-0 z-[55]"
            aria-hidden="true"
            onClick={() => setDrawerOpen(false)}
          />
        )}
        <main
          className="flex min-h-0 min-w-0 flex-col overflow-hidden"
          inert={drawerOpen}
        >
          {development ? <DevelopmentShell /> : <Outlet />}
        </main>
      </div>
    </PhoneBarSlot.Provider>
  );
};

const Shell = () => {
  useVisibleViewport();
  return (
    <ViewDataProvider>
      <ConversationsProvider>
        <WorkspaceProvider>
          <HouseholdShell />
        </WorkspaceProvider>
      </ConversationsProvider>
    </ViewDataProvider>
  );
};

export const appRoutes: RouteObject[] = [
  {
    path: "/",
    element: <Shell />,
    children: [
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
      {
        path: "notification-destinations",
        element: <NotificationDestinationsPage />,
      },
      {
        path: "notification-destinations/:destinationId",
        element: <NotificationDestinationDetailPage />,
      },
      { path: "notifications", element: <NotificationsPage /> },
      { path: "notifications/:attemptId", element: <NotificationsPage /> },
      { path: "nats", element: <NatsPage /> },
      { path: "device-facts", element: <DeviceFactsPage /> },
    ],
  },
];

interface AppProps {
  /** The caller creates and disposes this router. The browser uses hash routing; embedded previews use memory routing. */
  router: RouterProviderProps["router"];
}

export default function App({ router }: AppProps) {
  return <RouterProvider router={router} />;
}

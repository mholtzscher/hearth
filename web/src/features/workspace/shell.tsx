import { MenuIcon, PlusIcon } from "lucide-react";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { RefObject } from "react";
import { flushSync } from "react-dom";
import { Outlet, useLocation, useMatch } from "react-router-dom";

import { Button } from "../../components/button";
import { cn } from "../../lib/utils";
import { useConversations } from "../conversation/conversations-provider";
import { DevelopmentShell } from "./development-shell";
import { PhoneBarSlot } from "./phone-bar-slot";
import { serverLabels } from "./server-status";
import type { ServerState } from "./server-status";
import { HouseholdSidebar } from "./sidebar";
import type { NavMode } from "./sidebar";
import { useWorkspace } from "./workspace";

const pageTitle = (home: boolean, development: boolean): string => {
  if (home) {
    return "Home";
  }
  if (development) {
    return "Development";
  }
  return "Dashboards";
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
          className="text-body text-foreground truncate font-semibold group-has-[[data-slot=phone-bar-slot]:not(:empty)]/phone-bar:hidden"
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

export const HouseholdShell = () => {
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

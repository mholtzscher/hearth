import {
  HistoryIcon,
  HouseIcon,
  LayoutDashboardIcon,
  PlusIcon,
  WrenchIcon,
  XIcon,
} from "lucide-react";
import type { RefObject } from "react";
import { Link, NavLink } from "react-router-dom";

import { Button } from "../../components/button";
import { cn } from "../../lib/utils";
import { ConversationHistory } from "../conversation/conversation-history";
import { ServerStatus } from "./server-status";
import type { ServerState } from "./server-status";

export type NavMode = "full" | "rail" | "bar";

const navIconClass =
  "size-[18px] shrink-0 group-aria-[current=page]:text-violet";

export const navLinkClass = (nav: NavMode): string =>
  cn(
    "group text-muted-foreground hover:bg-gray-2 hover:text-foreground aria-[current=page]:bg-gray-2 aria-[current=page]:text-foreground flex min-h-11 items-center gap-3 rounded-md p-[11px] no-underline",
    nav === "rail" && "text-caption flex-col justify-center gap-1.5 px-1 py-3",
    nav === "bar" && "hover:bg-gray-3 aria-[current=page]:bg-gray-3"
  );

const hideWhileSearchingClass = (nav: NavMode): string | undefined =>
  nav === "bar"
    ? "group-has-[[data-slot=history-search]:focus-within]/sidebar:hidden group-has-[[data-slot=history-search]_input:not(:placeholder-shown)]/sidebar:hidden"
    : undefined;

const historyClassNames = (nav: NavMode, drawerOpen: boolean) => {
  let root: string | undefined;
  if (nav === "rail") {
    root = cn(
      "border-border bg-card fixed inset-y-0 left-[104px] z-[60] w-[min(320px,85vw)] border-t-0 border-r px-3 pt-3 pb-4 shadow-[12px_0_40px_#0006] [--history-bg:var(--gray-1)]",
      drawerOpen
        ? "slide-drawer-in visible translate-x-0 opacity-100"
        : "slide-drawer-out invisible -translate-x-3 opacity-0"
    );
  } else if (nav === "bar") {
    root =
      "pt-4 [--history-bg:var(--gray-1)] has-[input:focus]:border-t-0 has-[input:focus]:pt-3 has-[input:not(:placeholder-shown)]:border-t-0 has-[input:not(:placeholder-shown)]:pt-3";
  }
  return {
    root,
    header: nav === "rail" ? "pe-0 pb-1" : undefined,
    close: nav === "rail" ? "-me-1" : undefined,
  };
};

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
      "px-2 text-[24px] leading-normal font-semibold tracking-[-.03em]",
      nav === "rail" && "text-center text-[0px]",
      nav === "bar" && "min-h-11 ps-3 pe-[52px] pt-2 text-[20px] leading-7",
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
        "text-label text-muted-foreground mt-1.5 block font-normal",
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

export const HouseholdSidebar = ({
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
          "bg-card fixed inset-y-0 left-0 z-[60] w-[min(320px,85vw)] gap-4 px-3 pt-4 pb-[max(16px,var(--safe-bottom,env(safe-area-inset-bottom)))] shadow-[12px_0_40px_#0006]",
        nav === "bar" &&
          (drawerOpen
            ? "slide-drawer-in visible translate-x-0 opacity-100"
            : "slide-drawer-out invisible -translate-x-3 opacity-0")
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

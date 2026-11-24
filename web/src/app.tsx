import { Navigate, RouterProvider } from "react-router-dom";
import type { RouteObject, RouterProviderProps } from "react-router-dom";

import { ConversationsProvider } from "./features/conversation/conversations-provider.tsx";
import { Canvas } from "./features/dashboards/canvas.tsx";
import { DashboardLibrary } from "./features/dashboards/dashboard-library.tsx";
import { WorkspaceProvider } from "./features/dashboards/workspace.tsx";
import { ViewDataProvider } from "./features/views/data/provider.tsx";
import { HouseholdShell } from "./features/workspace/shell.tsx";
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

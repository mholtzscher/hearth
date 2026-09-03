import { AppBar, Box, Button, Container, TextField, Toolbar, Typography } from "@mui/material";
import { useState } from "react";
import { HashRouter, Link as RouterLink, Route, Routes, useNavigate } from "react-router-dom";
import { api, ApiError, getBaseUrl, setBaseUrl } from "./api/client.ts";
import { usePolling } from "./api/hooks.ts";
import { StatusChip } from "./components/common.tsx";
import AdaptersPage from "./pages/AdaptersPage.tsx";
import CommandsPage from "./pages/CommandsPage.tsx";
import DevicesPage from "./pages/DevicesPage.tsx";
import EntitiesPage from "./pages/EntitiesPage.tsx";
import EntityDetailPage from "./pages/EntityDetailPage.tsx";
import NatsPage from "./pages/NatsPage.tsx";

function HealthBadges() {
  const health = usePolling("healthz", api.health, 10_000);
  const ready = usePolling("readyz", api.ready, 10_000);
  return (
    <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
      <StatusChip label="healthz" status={readyStatus(health.data?.status, health.error)} />
      <StatusChip label="readyz" status={readyStatus(ready.data?.status, ready.error)} />
    </Box>
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
    <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
      <TextField
        size="small"
        label="Jump to entity id"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && value.trim()) navigate(`/entities/${value.trim()}`);
        }}
        sx={{ minWidth: 240 }}
      />
      <Button
        size="small"
        variant="outlined"
        onClick={() => value.trim() && navigate(`/entities/${value.trim()}`)}
      >
        Go
      </Button>
    </Box>
  );
}

function Shell() {
  const [baseUrl, setBaseUrlState] = useState(getBaseUrl());
  return (
    <Box>
      <AppBar position="static">
        <Toolbar sx={{ gap: 2, flexWrap: "wrap" }}>
          <Typography variant="h6">Hearth Debug</Typography>
          <Button color="inherit" component={RouterLink} to="/entities">
            Entities
          </Button>
          <Button color="inherit" component={RouterLink} to="/devices">
            Devices
          </Button>
          <Button color="inherit" component={RouterLink} to="/adapters">
            Adapters
          </Button>
          <Button color="inherit" component={RouterLink} to="/commands">
            Commands
          </Button>
          <Button color="inherit" component={RouterLink} to="/nats">
            NATS
          </Button>
          <Box sx={{ flexGrow: 1 }} />
          <HealthBadges />
        </Toolbar>
        <Toolbar variant="dense" sx={{ gap: 1.5, bgcolor: "background.paper", color: "text.primary" }}>
          <TextField
            size="small"
            label="hearthd base URL (empty = same origin)"
            placeholder="http://127.0.0.1:8080"
            value={baseUrl}
            onChange={(e) => {
              setBaseUrlState(e.target.value);
              setBaseUrl(e.target.value.trim());
            }}
            sx={{ minWidth: 300 }}
          />
          <EntityJump />
          <Button
            size="small"
            href={`${baseUrl || ""}/openapi.json`}
            target="_blank"
            rel="noreferrer"
          >
            OpenAPI JSON
          </Button>
        </Toolbar>
      </AppBar>
      <Container maxWidth="lg" sx={{ py: 3 }}>
        <Routes>
          <Route path="/" element={<EntitiesPage />} />
          <Route path="/entities" element={<EntitiesPage />} />
          <Route path="/entities/:entityId" element={<EntityDetailPage />} />
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/adapters" element={<AdaptersPage />} />
          <Route path="/commands" element={<CommandsPage />} />
          <Route path="/nats" element={<NatsPage />} />
        </Routes>
      </Container>
    </Box>
  );
}

export default function App() {
  return (
    <HashRouter>
      <Shell />
    </HashRouter>
  );
}

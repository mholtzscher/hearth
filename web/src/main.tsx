import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createHashRouter } from "react-router-dom";

import App, { appRoutes } from "./app.tsx";

import "./index.css";

const root = document.querySelector("#root");
if (!root) {
  throw new Error("missing #root");
}
const router = createHashRouter(appRoutes);
createRoot(root).render(
  <StrictMode>
    <App router={router} />
  </StrictMode>
);

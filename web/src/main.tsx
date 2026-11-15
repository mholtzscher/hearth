import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createHashRouter } from "react-router-dom";

import App, { appRoutes } from "./app.tsx";

import "./index.css";

// Hearth is dark-only; shadcn components select their dark: variants from this class.
document.documentElement.classList.add("dark");

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

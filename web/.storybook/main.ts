import type { StorybookConfig } from "@storybook/react-vite";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";
import { exactHost, parsePort } from "../scripts/storybook-server.ts";

const allowedHost = process.env.HEARTH_STORYBOOK_ALLOWED_HOST;
const allowedHosts = allowedHost ? [exactHost(allowedHost)] : [];
const httpsPort = process.env.HEARTH_STORYBOOK_HTTPS_PORT;

const config: StorybookConfig = {
  stories: [
    "../src/**/*.mdx",
    "../src/**/*.stories.@(js|jsx|mjs|ts|tsx)"
  ],
  addons: [
    "@storybook/addon-vitest",
    "@storybook/addon-a11y",
    "@storybook/addon-docs",
    "@storybook/addon-mcp",
    "msw-storybook-addon"
  ],
  framework: {
    name: "@storybook/react-vite",
    // Never inherit the app's live Core/NATS proxies.
    options: { builder: { viteConfigPath: false } },
  },
  staticDirs: ["./public"],
  features: { componentsManifest: true },
  typescript: {
    reactDocgen: "react-docgen-typescript",
    // These options replace Storybook's defaults, so restate the ones controls rely on.
    reactDocgenTypescriptOptions: {
      shouldExtractLiteralValuesFromEnum: true,
      shouldRemoveUndefinedFromOptional: true,
      savePropValueAsString: true,
      // The manifest drops inherited sources over 30 props. Keep a useful native subset.
      propFilter: (prop) => !prop.parent?.fileName.includes("node_modules") || [
        "id", "role", "tabIndex", "name", "required", "readOnly", "min", "max", "step",
        "aria-label", "aria-labelledby", "aria-describedby", "aria-invalid", "aria-errormessage",
        "aria-required", "aria-disabled", "aria-expanded", "aria-controls", "aria-busy",
        "disabled", "type", "value", "defaultValue",
        "placeholder", "onChange", "onClick", "children", "checked", "defaultChecked",
        "onCheckedChange",
        "open", "defaultOpen", "onOpenChange", "modal", "htmlFor",
        "collapsible", "onValueChange", "orientation", "dir",
      ].includes(prop.name),
    },
  },
  core: { disableTelemetry: true, disableWhatsNewNotifications: true, allowedHosts },
  viteFinal(config) {
    // Prebundle shared dependencies so late discovery cannot reload an active test.
    config.optimizeDeps = {
      ...config.optimizeDeps,
      include: [...(config.optimizeDeps?.include ?? []), "prism-react-renderer", "cn/config"],
    };
    config.publicDir = path.resolve(import.meta.dirname, "public");
    config.plugins = [...(config.plugins ?? []), tailwindcss()];
    config.resolve = {
      ...config.resolve,
      alias: { "@": path.resolve(import.meta.dirname, "../src") },
    };
    config.server = {
      ...config.server,
      allowedHosts,
      // The browser must connect through HTTPS, not to Vite's loopback backend port.
      ...(allowedHost && httpsPort ? {
        hmr: {
          ...(typeof config.server?.hmr === "object" ? config.server.hmr : {}),
          host: allowedHost,
          protocol: "wss",
          clientPort: parsePort(httpsPort),
        },
      } : {}),
      proxy: {},
      fs: { allow: [path.resolve(import.meta.dirname, "../..")] },
    };
    return config;
  },
};
export default config;

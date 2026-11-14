import type { StorybookConfig } from "@storybook/react-vite";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

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
      ].includes(prop.name),
    },
  },
  core: { disableTelemetry: true, disableWhatsNewNotifications: true },
  viteFinal(config) {
    config.publicDir = path.resolve(import.meta.dirname, "public");
    config.plugins = [...(config.plugins ?? []), tailwindcss()];
    config.resolve = {
      ...config.resolve,
      alias: { "@": path.resolve(import.meta.dirname, "../src") },
    };
    config.server = {
      ...config.server,
      proxy: {},
      fs: { allow: [path.resolve(import.meta.dirname, "../..")] },
    };
    return config;
  },
};
export default config;

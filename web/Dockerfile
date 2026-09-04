# Build the Vite SPA with the repo's Node toolchain (see root mise.toml).
FROM node:26-alpine AS build
# pnpm major-pinned to the lockfile writer (local `pnpm --version`); corepack
# no longer ships with Node.
RUN npm install -g pnpm@11
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm run build

# Serve the static bundle, proxying the API to hearthd so the dashboard works
# same-origin with no CORS (mirrors the vite dev proxy in vite.config.ts).
FROM nginx:1.29-alpine
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
ENV HEARTHD_URL=http://127.0.0.1:8080 \
    NATS_MONITOR_URL=http://127.0.0.1:8222
EXPOSE 80

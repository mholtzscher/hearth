# Build with repository root as context: docker build -f bridges/homekit/Dockerfile .
FROM node:26.6.0-bookworm-slim AS build
RUN npm install --global pnpm@11.25.0
WORKDIR /build/bridges/homekit
COPY bridges/homekit/package.json bridges/homekit/pnpm-lock.yaml bridges/homekit/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY bridges/homekit/tsconfig.json ./
COPY bridges/homekit/scripts ./scripts
COPY bridges/homekit/src ./src
COPY entitytypes/powerv1 /build/entitytypes/powerv1
COPY entitytypes/brightnessv1 /build/entitytypes/brightnessv1
RUN pnpm build && pnpm prune --prod

FROM node:26.6.0-bookworm-slim
RUN apt-get update && apt-get install --yes --no-install-recommends util-linux \
    && rm -rf /var/lib/apt/lists/*
ENV NODE_ENV=production
WORKDIR /app
COPY --from=build --chown=node:node /build/bridges/homekit/dist ./dist
COPY --from=build --chown=node:node /build/bridges/homekit/node_modules ./node_modules
COPY --from=build --chown=node:node /build/bridges/homekit/package.json ./package.json
USER node
ENTRYPOINT ["node", "dist/cli.js"]

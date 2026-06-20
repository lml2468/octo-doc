# Multi-stage build. A build stage compiles TS → dist; the runtime stage carries
# ONLY prod deps + dist (no dev deps, no toolchain, no sources). node:sqlite is
# built into Node 22, so there is NO native compilation step — the tiny Alpine
# base works (no glibc needed). Result is ~55 MB compressed; the Node 22 runtime
# binary is the size floor, and we avoid the dev-dep bloat that balloons images.

# ── deps stage: production dependencies only ─────────────────────────────────
FROM node:22-alpine AS deps
WORKDIR /app
ENV NODE_ENV=production
RUN corepack enable
# Tolerate flaky registries (low-end VPS / restricted networks).
ENV PNPM_CONFIG_FETCH_RETRIES=5 \
    PNPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=120000 \
    PNPM_CONFIG_NETWORK_CONCURRENCY=4
COPY package.json pnpm-lock.yaml* ./
# Default image is the slim sqlite+fs stack — optional adapters (pg, @aws-sdk)
# are NOT installed. Pass --build-arg WITH_OPTIONAL=1 for the postgres+s3 image.
ARG WITH_OPTIONAL=0
RUN if [ "$WITH_OPTIONAL" = "1" ]; then \
      pnpm install --prod --frozen-lockfile 2>/dev/null || pnpm install --prod ; \
    else \
      pnpm install --prod --no-optional --frozen-lockfile 2>/dev/null || pnpm install --prod --no-optional ; \
    fi

# ── build stage: compile TS → dist (needs dev deps) ──────────────────────────
FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
ENV PNPM_CONFIG_FETCH_RETRIES=5 PNPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=120000
COPY package.json pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile 2>/dev/null || pnpm install
COPY tsconfig.json tsup.config.ts ./
COPY src ./src
RUN pnpm build

# ── runtime stage ────────────────────────────────────────────────────────────
FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production \
    PORT=8080 \
    HOST=0.0.0.0 \
    DATA_DIR=/data \
    STORAGE=sqlite+fs

# Non-root runtime user; /data is the persisted volume (node:alpine ships `node`).
RUN mkdir -p /data && chown -R node:node /data
COPY --from=deps --chown=node:node /app/node_modules ./node_modules
COPY --from=build --chown=node:node /app/dist ./dist
COPY --chown=node:node package.json ./
COPY --chown=node:node migrations ./migrations

USER node
VOLUME ["/data"]
EXPOSE 8080

# Container healthcheck hits /healthz (orchestrator-friendly).
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||8080)+'/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "dist/index.js"]

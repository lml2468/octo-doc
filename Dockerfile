# Multi-stage build. Final image carries ONLY prod deps + source — no dev deps,
# no build toolchain. node:sqlite is built into Node 22, so there is NO native
# compilation step, which lets the tiny Alpine base work (no glibc needed). The
# result is ~55 MB compressed (the Node 22 runtime binary is the size floor;
# the win is avoiding the dev-dep/toolchain bloat that balloons images >500 MB).

# ── deps stage: install production dependencies only ─────────────────────────
FROM node:22-alpine AS deps
WORKDIR /app
ENV NODE_ENV=production
RUN corepack enable
# Tolerate flaky registries (low-end VPS / restricted networks): more retries
# and a longer fetch timeout so a transient ECONNRESET doesn't fail the build.
ENV PNPM_CONFIG_FETCH_RETRIES=5 \
    PNPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=120000 \
    PNPM_CONFIG_NETWORK_CONCURRENCY=4
COPY package.json pnpm-lock.yaml* ./
# Prod deps only. Default image is the slim sqlite+fs stack — optional adapters
# (pg, @aws-sdk) are NOT installed, keeping the image small. To build the fat
# image that also supports STORAGE=postgres+s3, pass --build-arg WITH_OPTIONAL=1.
ARG WITH_OPTIONAL=0
RUN if [ "$WITH_OPTIONAL" = "1" ]; then \
      pnpm install --prod --frozen-lockfile 2>/dev/null || pnpm install --prod ; \
    else \
      pnpm install --prod --no-optional --frozen-lockfile 2>/dev/null || pnpm install --prod --no-optional ; \
    fi

# ── runtime stage ────────────────────────────────────────────────────────────
FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production \
    PORT=8080 \
    HOST=0.0.0.0 \
    DATA_DIR=/data \
    STORAGE=sqlite+fs

# Non-root runtime user; /data is the persisted volume. (node:alpine ships a
# `node` user already.)
RUN mkdir -p /data && chown -R node:node /data
COPY --from=deps --chown=node:node /app/node_modules ./node_modules
COPY --chown=node:node package.json ./
COPY --chown=node:node src ./src
COPY --chown=node:node migrations ./migrations
COPY --chown=node:node bin ./bin

USER node
VOLUME ["/data"]
EXPOSE 8080

# Container healthcheck hits /healthz (orchestrator-friendly).
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||8080)+'/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "src/index.js"]

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM node:20-alpine AS builder

# better-sqlite3 requires native compilation
RUN apk add --no-cache python3 make g++

WORKDIR /app

# Install deps first for layer caching
COPY package*.json ./
COPY shared/package.json ./shared/
COPY backend/package.json ./backend/
COPY frontend/package.json ./frontend/
RUN npm ci

# Build all workspaces
COPY shared/ ./shared/
COPY backend/ ./backend/
COPY frontend/ ./frontend/
RUN npm run build

# ── Stage 2: production ────────────────────────────────────────────────────────
FROM node:20-alpine

WORKDIR /app

# Install prod deps (includes native rebuild for better-sqlite3), then drop build tools
COPY package*.json ./
COPY shared/package.json ./shared/
COPY backend/package.json ./backend/
COPY frontend/package.json ./frontend/
RUN apk add --no-cache --virtual .build-deps python3 make g++ && \
    npm ci --omit=dev && \
    apk del .build-deps

# Copy compiled artifacts from builder
COPY --from=builder /app/shared/dist ./shared/dist
COPY --from=builder /app/backend/dist ./backend/dist
COPY --from=builder /app/frontend/dist ./frontend/dist

ENV NODE_ENV=production
ENV DB_PATH=/data/rollarr.db

RUN mkdir /data && chown node:node /data

EXPOSE 3001

VOLUME ["/data"]

USER node

CMD ["node", "backend/dist/index.js"]

# Stage 1: Build frontend (npm workspaces: shared + frontend)
FROM node:20-alpine AS frontend-builder
WORKDIR /app
# Lockfile + workspace manifests first for layer caching.
COPY package.json package-lock.json ./
COPY shared/package.json ./shared/
COPY frontend/package.json ./frontend/
RUN npm ci
COPY shared/ ./shared/
COPY frontend/ ./frontend/
# shared must be compiled before frontend resolves @rollarr/shared.
RUN npm run build -w shared && npm run build -w frontend

# Stage 2: Build Go binary
FROM golang:1.23-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist
RUN go build -o /rollarr ./cmd/rollarr/

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=go-builder /rollarr /rollarr
ENTRYPOINT ["/rollarr"]

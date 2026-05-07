# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

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

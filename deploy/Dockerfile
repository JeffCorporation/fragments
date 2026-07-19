# Multi-stage build: Vue SPA → Go binary (with the SPA embedded) → distroless.
# Build from the repo root:  docker build -f deploy/Dockerfile -t fragments .

# --- Stage 1: build the Vue SPA ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Stage 2: build the static Go binary with the embedded SPA ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Drop in the freshly built SPA so go:embed picks it up.
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /fragments ./cmd/fragments

# --- Stage 3: minimal runtime (no shell, no libc — pure-Go binary) ---
FROM gcr.io/distroless/static-debian12
COPY --from=build /fragments /fragments
VOLUME ["/data"]
EXPOSE 8080
# Config comes from the environment (see .env / docker-compose env_file).
ENTRYPOINT ["/fragments", "serve", "-data", "/data", "-addr", "0.0.0.0:8080"]

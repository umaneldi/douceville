# syntax=docker/dockerfile:1

# Stage 1: build the static Go binary.
FROM golang:1.26 AS build

WORKDIR /src

# Download dependencies first so they are cached as long as go.mod/go.sum are unchanged.
COPY go.mod go.sum ./
RUN go mod download

# Copy the source and build. The web/ assets and SQL migrations are embedded
# into the binary via //go:embed, so they must be present at compile time.
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

# CGO disabled -> fully static binary (pgx is pure Go), usable in a minimal base image.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/douceville ./cmd/server

# Stage 2: minimal runtime image.
# distroless/static ships CA certificates (needed for HTTPS calls to OpenRouteService)
# and runs as a non-root user by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/douceville /douceville

# Runtime configuration (POSTGRES_*, ADDOK_HOST, OPENROUTESERVICE_KEY,
# JWT_SECRET, ...) is provided via the container environment
# (see docker-compose.yml), not baked into the image.
# The single binary serves both the UI (at /) and the JSON API (at /api).
EXPOSE 3566

ENTRYPOINT ["/douceville"]

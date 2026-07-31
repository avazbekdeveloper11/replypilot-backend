# syntax=docker/dockerfile:1

# --- builder ---------------------------------------------------------------
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod ./
# go.sum is generated locally by `go mod tidy` (see README "Known
# limitations" — it was intentionally not hand-written) and is not yet
# present in this repo snapshot. The glob copy below succeeds whether or
# not the file exists; once you've run `go mod tidy` and committed go.sum,
# this picks it up automatically with no Dockerfile change needed.
COPY go.su[m] ./
RUN go mod download

COPY . .
# Build all four binaries. The migrate tool embeds internal/migrations/*.sql
# (see internal/migrations/embed.go), so the migrate binary is fully
# self-contained — no .sql files need to exist in the runtime image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker-ai ./cmd/worker-ai && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/token-refresh ./cmd/token-refresh

# --- runtime -----------------------------------------------------------------
# distroless static: no shell, no package manager, runs as a non-root user
# by default — meaningfully smaller attack surface than alpine/debian-slim
# for a statically-linked Go binary that needs nothing else at runtime.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# All four binaries ship in the one image. The api service uses the default
# entrypoint; docker-compose's `migrate`, `worker-ai`, and `token-refresh`
# services override it to /migrate, /worker-ai, and /token-refresh
# respectively — one image, four entrypoints, rather than separate
# builds/registries for what's the same module.
COPY --from=builder /out/api /api
COPY --from=builder /out/migrate /migrate
COPY --from=builder /out/worker-ai /worker-ai
COPY --from=builder /out/token-refresh /token-refresh

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/api"]

# Starter key-value store (WAL + B+ tree) compiled to a static binary on a
# distroless base. The WAL lives in DATA_DIR; mount a volume there to keep it.

# ---- build ------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

# ---- runtime: no shell, no package manager, runs as uid 65532 ---------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/app /app
ENV PORT=8080 DATA_DIR=/tmp/kvdb
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app"]

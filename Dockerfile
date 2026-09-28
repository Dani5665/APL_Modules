# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies are copied first so a source-only change reuses this layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO is off, so the result is a single static binary that runs on
# distroless. -trimpath drops build paths, -s -w drop the symbol table.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w" \
      -o /out/app ./cmd/app

# distroless has no shell to mkdir with, and the container runs as the
# nonroot user below. A named volume with no prior content is created by the
# Docker engine owned by root, which nonroot cannot write into - so an empty
# directory owned by nonroot (65532:65532, distroless's fixed uid/gid) is
# baked into the image here; Docker copies a bind mount point's ownership
# from the image the first time a volume is attached at that path.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# ---- run ----
FROM gcr.io/distroless/static-debian12:nonroot

# The templates, the stylesheet and the vendored JavaScript are embedded in
# the binary, so nothing else is copied in.
COPY --from=build /out/app /app
COPY --from=build --chown=65532:65532 /out/data /data

# SQLite, its WAL files and the backups live on this volume.
VOLUME /data

ENV APP_ADDR=:8080 \
    DATA_DIR=/data

EXPOSE 8080
USER nonroot:nonroot

# distroless has no shell or curl, so the binary checks itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app", "healthcheck"]

ENTRYPOINT ["/app"]

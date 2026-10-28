# syntax=docker/dockerfile:1.7
# Multi-stage build: one builder compiles every Go binary, each runtime target
# copies a single static binary into a distroless image running as non-root.

ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    for cmd in api migrate simulator verify; do \
      go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/$cmd ./cmd/$cmd || exit 1; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS migrate
COPY --from=build /out/migrate /migrate
USER nonroot:nonroot
ENTRYPOINT ["/migrate"]
CMD ["up"]

FROM gcr.io/distroless/static-debian12:nonroot AS simulator
COPY --from=build /out/simulator /simulator
USER nonroot:nonroot
ENTRYPOINT ["/simulator"]

FROM gcr.io/distroless/static-debian12:nonroot AS verify
COPY --from=build /out/verify /verify
USER nonroot:nonroot
ENTRYPOINT ["/verify"]

# api is the last stage so a plain `docker build` produces the API image.
FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 CMD ["/api", "healthcheck"]
ENTRYPOINT ["/api"]

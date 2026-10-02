# syntax=docker/dockerfile:1

# Builds one of the binaries in cmd/, chosen with --build-arg CMD=kart-api or
# CMD=coupons-server, into a distroless image that runs as a non-root user.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
ARG CMD
RUN test -n "$CMD" || { echo "set --build-arg CMD=<binary in cmd/>"; exit 1; }
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/$CMD

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
ENTRYPOINT ["/server"]

# syntax=docker/dockerfile:1

# Builds one of the binaries in api/, chosen with --build-arg CMD=kart-api or
# CMD=coupons-server, into a distroless image that runs as a non-root user.
# The image carries .env.defaults; override settings with environment
# variables or an ENV_FILE.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY pkg ./pkg
ARG CMD
RUN test -n "$CMD" || { echo "set --build-arg CMD=<binary in api/>"; exit 1; }
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./api/$CMD

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY .env.defaults ./
COPY --from=build /out/server /server
USER nonroot:nonroot
ENTRYPOINT ["/server"]

# syntax=docker/dockerfile:1

# Shared compile stage: builds both binaries once; downstream targets
# reuse this layer cache.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify

# Shared runtime base.
FROM alpine:3.21 AS base
RUN adduser -D -u 10001 app

# API image.
FROM base AS server
COPY --from=build /out/server /usr/local/bin/server
USER app
EXPOSE 8080
ENTRYPOINT ["server"]

# One-shot acceptance image.
FROM base AS verify
COPY --from=build /out/verify /usr/local/bin/verify
USER app
ENTRYPOINT ["verify"]

FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY services/notification/go.mod services/notification/go.sum ./services/notification/
RUN --mount=type=cache,target=/go/pkg/mod cd services/notification && go mod download
COPY services/notification ./services/notification
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/notification && CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3   CMD wget -qO- http://localhost:8090/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]

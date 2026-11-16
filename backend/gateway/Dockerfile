FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY gateway/go.mod gateway/go.sum ./gateway/
RUN --mount=type=cache,target=/go/pkg/mod cd gateway && go mod download
COPY gateway ./gateway
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd gateway && CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]

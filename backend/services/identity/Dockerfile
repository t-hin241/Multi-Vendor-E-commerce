FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY services/identity/go.mod services/identity/go.sum ./services/identity/
RUN --mount=type=cache,target=/go/pkg/mod cd services/identity && go mod download
COPY services/identity ./services/identity
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/identity && CGO_ENABLED=0 go build -o /out/server ./cmd/server && CGO_ENABLED=0 go build -o /out/bootstrap-admin ./cmd/bootstrap-admin && CGO_ENABLED=0 go build -o /out/bootstrap-access ./cmd/bootstrap-access

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
COPY --from=builder /out/bootstrap-admin /usr/local/bin/bootstrap-admin
COPY --from=builder /out/bootstrap-access /usr/local/bin/bootstrap-access
USER app
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3   CMD wget -qO- http://localhost:8081/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]

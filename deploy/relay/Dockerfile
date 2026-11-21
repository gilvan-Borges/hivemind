# Imagem mínima do hivemind-relay.
# Construa num computador com RAM de sobra, a partir da raiz do repositório:
#   docker build -f deploy/relay/Dockerfile -t hivemind-relay .
# Não construa na VM Always Free de 1 GB. Na VM, prefira o binário estático
# e o Caddy no host (docs/GUIA_ORACLE_RELAY.md).

# Digests pinados (Spec 10 §3.2.3): tag mutável nunca mais vira base de build.
# Para renovar: docker pull golang:1.26.10-bookworm alpine:3.20 e atualize os
# digests manifest-list abaixo (docker manifest inspect <img>).
FROM golang:1.26.10-bookworm@sha256:5de7c99eb78ca5e515a4c340235d0955ec5964a5071c492c15503d7c267da473 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY relaywire ./relaywire
COPY VERSION ./VERSION
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X github.com/gilvan-Borges/HiveMind/internal/version.Version=$(cat VERSION) -X github.com/gilvan-Borges/HiveMind/internal/version.Commit=docker" \
    -o /out/hivemind-relay ./cmd/hivemind-relay

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 65532 relay \
    && mkdir -p /var/lib/hivemind-relay \
    && chown relay:relay /var/lib/hivemind-relay
USER relay
WORKDIR /var/lib/hivemind-relay
COPY --from=build /out/hivemind-relay /usr/local/bin/hivemind-relay
EXPOSE 8787
# O relay responde GET /api/v1/health; o estado do container segue o estado real
# do serviço, não apenas "processo vivo".
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8787/api/v1/health || exit 1
ENV HIVEMIND_RELAY_ADDR=0.0.0.0:8787 \
    HIVEMIND_RELAY_DATA=/var/lib/hivemind-relay
ENTRYPOINT ["/usr/local/bin/hivemind-relay"]

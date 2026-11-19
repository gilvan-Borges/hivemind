# Imagem mínima do hivemind-relay.
# Construa num computador com RAM de sobra, a partir da raiz do repositório:
#   docker build -f deploy/relay/Dockerfile -t hivemind-relay .
# Não construa na VM Always Free de 1 GB. Na VM, prefira o binário estático
# e o Caddy no host (docs/GUIA_ORACLE_RELAY.md).

FROM golang:1.26.10-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY relaywire ./relaywire
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/hivemind-relay ./cmd/hivemind-relay

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 65532 relay \
    && mkdir -p /var/lib/hivemind-relay \
    && chown relay:relay /var/lib/hivemind-relay
USER relay
WORKDIR /var/lib/hivemind-relay
COPY --from=build /out/hivemind-relay /usr/local/bin/hivemind-relay
EXPOSE 8787
ENV HIVEMIND_RELAY_ADDR=0.0.0.0:8787 \
    HIVEMIND_RELAY_DATA=/var/lib/hivemind-relay
ENTRYPOINT ["/usr/local/bin/hivemind-relay"]

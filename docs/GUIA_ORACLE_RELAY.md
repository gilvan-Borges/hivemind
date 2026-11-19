# Relay na Oracle Cloud Always Free

Este guia sobe o `hivemind-relay` numa VM Always Free para o app controlar a IDE de qualquer rede. O contrato está em [`SPEC_RELAY.md`](SPEC_RELAY.md).

A VM típica é a **VM.Standard.E2.1.Micro**: 1/8 OCPU, 1 GB de RAM, **x86_64**. A alternativa Ampere **A1.Flex** (ARM) também serve, desde que você use o binário `linux/arm64`. Não compile o relay em cima dessa VM: o SQLite embutido é Go puro e o compilador passa fácil da memória disponível. Gere o binário num computador maior, ou baixe o artefato do GitHub Actions (`hivemind-relay-linux-amd64` ou `hivemind-relay-linux-arm64`).

O processo escuta só em `127.0.0.1:8787`. Quem fala com a internet é o Caddy, com HTTPS automático. A IDE e o celular abrem conexão de saída; você não abre porta no roteador de casa.

## 1. Criar a VM

1. No console Oracle Cloud: **Compute → Instances → Create instance**.
2. Image: **Ubuntu 24.04** (Canonical). Shape: **VM.Standard.E2.1.Micro** (Always Free) ou Ampere A1.
3. Baixe a chave SSH privada e anote o usuário padrão (`ubuntu`).
4. Anote o **IP público**.

## 2. Liberar 80 e 443

O Caddy precisa da porta 80 para o desafio HTTP-01 e da 443 para o tráfego. As duas têm de estar abertas **na Security List e no firewall do sistema**. A Security List sozinha não basta: a imagem da Oracle costuma barrar o pacote no `iptables` mesmo com a lista aberta.

### Security List

**Networking → Virtual cloud networks → a VCN da VM → Security Lists → a lista da subnet → Add Ingress Rules.** Crie duas regras:

| Source | Protocol | Porta |
|---|---|---|
| `0.0.0.0/0` | TCP | 80 |
| `0.0.0.0/0` | TCP | 443 |

Se a subnet tiver IPv6 e você for usar, repita com `::/0`. Não abra a 8787 para a internet.

### Firewall da VM

Entre por SSH e rode:

```bash
sudo apt-get update
sudo apt-get install -y iptables-persistent
sudo iptables -I INPUT -p tcp -m state --state NEW -m tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT -p tcp -m state --state NEW -m tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
sudo iptables -L INPUT -n --line-numbers
```

Confira se as duas regras `ACCEPT` estão **acima** de qualquer `REJECT` genérico. Em Oracle Linux o equivalente é `firewall-cmd --permanent --add-service=http --add-service=https && firewall-cmd --reload`.

## 3. DNS

O HTTPS automático precisa de um nome apontando para o IP público.

- **Domínio que você já tem:** crie um registro `A` de `relay.seudominio` para o IP da VM. Espere o TTL.
- **Sem domínio:** crie um nome grátis no [DuckDNS](https://www.duckdns.org/) (`relayseunome.duckdns.org`) e ponha o IP da VM no campo de endereço. O Caddy emite o certificado por HTTP-01; não precisa do plugin de DNS.

Anote a URL final: `https://relay.seudominio` ou `https://relayseunome.duckdns.org`. Sem barra no final.

## 4. Binário e usuário

No seu computador (não na VM de 1 GB):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o hivemind-relay ./cmd/hivemind-relay
# Ampere A1:
# CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o hivemind-relay ./cmd/hivemind-relay
scp hivemind-relay ubuntu@IP_DA_VM:/tmp/hivemind-relay
```

`amd64` para a Micro x86. `arm64` para a Ampere A1. Trocar os dois deixa o `systemd` em loop de crash.

Na VM:

```bash
sudo useradd --system --create-home --home-dir /var/lib/hivemind-relay --shell /usr/sbin/nologin hivemind
sudo install -m 0755 /tmp/hivemind-relay /usr/local/bin/hivemind-relay
sudo chown root:root /usr/local/bin/hivemind-relay
rm /tmp/hivemind-relay
sudo install -d -m 0700 -o hivemind -g hivemind /var/lib/hivemind-relay
```

## 5. Senha inicial

```bash
sudo tee /etc/hivemind-relay.env >/dev/null <<'EOF'
HIVEMIND_RELAY_ADDR=127.0.0.1:8787
HIVEMIND_RELAY_DATA=/var/lib/hivemind-relay
HIVEMIND_RELAY_PUBLIC_URL=https://relay.seudominio
HIVEMIND_SEED_EMAIL=voce@exemplo
HIVEMIND_SEED_PASSWORD=
EOF
sudo chmod 600 /etc/hivemind-relay.env
```

Troque o e-mail e a URL. Deixe `HIVEMIND_SEED_PASSWORD` vazio para o processo gerar uma senha, **ou** escreva uma senha com pelo menos 10 caracteres. Não commite esse arquivo.

A unit está em [`deploy/relay/hivemind-relay.service`](../deploy/relay/hivemind-relay.service):

```bash
sudo cp deploy/relay/hivemind-relay.service /etc/systemd/system/hivemind-relay.service
sudo systemctl daemon-reload
sudo systemctl enable --now hivemind-relay
sudo journalctl -u hivemind-relay -n 40 --no-pager
```

Se a senha foi gerada, ela aparece uma vez no journal e em `/var/lib/hivemind-relay/initial-password.txt`. Copie para um gerenciador e apague o arquivo:

```bash
sudo shred -u /var/lib/hivemind-relay/initial-password.txt
```

Uma segunda subida não troca a senha. Para trocar depois, com a API no ar:

```bash
curl -sS -X POST https://relay.seudominio/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"voce@exemplo","password":"A-SENHA-ATUAL"}'
# use o accessToken:
curl -sS -X POST https://relay.seudominio/api/v1/auth/password \
  -H "Authorization: Bearer hmr_at_…" \
  -H 'Content-Type: application/json' \
  -d '{"currentPassword":"A-SENHA-ATUAL","newPassword":"UMA-SENHA-NOVA-LONGA"}'
```

## 6. Caddy

```bash
sudo apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt-get update
sudo apt-get install -y caddy
```

Substitua o Caddyfile pelo exemplo [`deploy/relay/Caddyfile`](../deploy/relay/Caddyfile), com o seu nome no lugar de `relay.example.com`:

```bash
sudo cp deploy/relay/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Teste, de fora da VM:

```bash
curl -sS https://relay.seudominio/api/v1/health
```

A resposta esperada é `{"ok":true,"service":"hivemind-relay",…}`. Se o certificado não sair, confira DNS (o nome tem de resolver para esse IP), as portas 80 e 443 na Security List **e** no `iptables`, e `journalctl -u caddy`.

## 7. Ligar a IDE e o celular

1. Na IDE, chip **mobile** → seção **Acesso remoto**.
2. URL `https://relay.seudominio`, o e-mail e a senha. **Entrar**. A senha não fica em disco; fica o refresh token em `relay.json` (modo `0600`, fora do Git).
3. **Ligar acesso remoto** registra este PC na conta e abre o WebSocket de saída. Ou gere um código/QR e confirme no celular (quando o app existir), ou digite o código que o celular mostrar.
4. A LAN (QR `hivemind://pair`) continua disponível e independente. Pode ficar desligada se você só for usar o relay.
5. No app: login na mesma conta, escolher a IDE na lista (`online: true`) e publicar a chave pública (§4 e §6 de `SPEC_RELAY.md`). Sem as duas chaves o relay não encaminha o plano de dados.

Login errado cinco vezes bloqueia o IP e o e-mail por 15 minutos. O relay também recusa mais de 10 tentativas de login por minuto por IP, antes de gastar CPU com a senha.

## 8. Operação

```bash
sudo systemctl status hivemind-relay
sudo journalctl -u hivemind-relay -f
```

Atualizar o binário: `scp` o arquivo novo para `/tmp`, `sudo install -m 0755` por cima de `/usr/local/bin/hivemind-relay` e `sudo systemctl restart hivemind-relay`. O SQLite em `/var/lib/hivemind-relay/relay.db` permanece. Faça uma cópia desse arquivo (e só dele; ele tem os hashes) antes de um upgrade maior.

Backup: pare o serviço, copie `relay.db` (e os `-wal`/`-shm` se existirem), suba de novo. Modo do diretório `0700`, do banco `0600`, dono `hivemind`.

Quem mais vai usar o relay: entre com o owner e `POST /api/v1/accounts` (veja a spec). Não há página de cadastro aberta.

## 9. Docker, se preferir construir fora da VM

Na máquina com RAM de sobra, na raiz do repositório:

```bash
docker build -f deploy/relay/Dockerfile -t hivemind-relay .
```

A imagem escuta 8787 e não inclui o Caddy. Na VM pequena continue com o binário estático + Caddy no host: é menos RAM do que um container extra. O Dockerfile existe para quem já opera com Docker num host maior.

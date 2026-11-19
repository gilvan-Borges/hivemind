# Spec — Relay HiveMind (app ↔ IDE de qualquer rede)

**Status:** contrato estável desta entrega  
**Binário:** `cmd/hivemind-relay`  
**Pacote compartilhado:** `relaywire/` (envelope, cifras, URI de vínculo)  
**Público:** o agente que implementar o cliente Android em `gilvan-Borges/HiveMind-App` deve conseguir fazê-lo só com este documento e com `docs/SPEC_APP_MOBILE_NATIVO.md` §5.

O relay é um processo pequeno, hospedado pelo dono (Oracle Cloud Always Free ou equivalente). A IDE e o app **só abrem conexões de saída**. Nenhum PC precisa de porta aberta na internet. O contrato HTTP+SSE do Remote Bridge (`/api/v1/projects`, `/runs/*`, `/approvals/resolve`, eventos) é tunelado; a lógica do app não muda entre LAN direta e relay.

---

## 1. Papéis e isolamento

```
App (Android)  --wss-->  Relay  <--wss--  IDE (PC de trabalho)
App (Android)  --wss-->  Relay  <--wss--  IDE (PC de casa)
```

Regras que o relay aplica em toda mensagem:

1. Uma conta nunca lista, revoga, vincula ou recebe quadros de IDE de outra conta. Consulta de IDE alheia responde `404 not_found` (não `403`), para não confirmar que o id existe.
2. O plano de dados de uma IDE nunca é entregue a outra IDE, mesmo da mesma conta. O app escolhe **uma** `deviceId` no `hello` e fica preso a ela até desconectar.
3. O relay só encaminha `app → IDE escolhida` e `IDE → apps que selecionaram essa IDE`. Não há canal IDE↔IDE nem app↔app.
4. Cada quadro com `deviceId` diferente do `hello` daquela conexão é recusado com `forbidden` e não é encaminhado.
5. Credencial de IDE (`hmr_dev_…`) só autentica `role: "ide"` daquele `deviceId`. Token de acesso (`hmr_at_…`) só autentica `role: "app"` e só para IDEs da própria conta.

O relay **não executa** o motor de agentes. Repositórios, modelos e segredos continuam no PC. O relay guarda contas, credenciais (só o hash), chaves **públicas**, a fila de eventos cifrados e RPCs cifrados pendentes.

---

## 2. Transporte

| Caminho | Uso |
|---|---|
| `https://<host>/api/v1/…` | Conta, sessão, lista de IDEs, vínculo |
| `wss://<host>/api/v1/ws` | Plano de dados (RPC + eventos) |

TLS é obrigatório fora de `localhost` / `127.0.0.1` / `::1`. O binário da IDE recusa `http://` em host público. Em produção o Caddy termina o HTTPS e faz proxy para `127.0.0.1:8787`. WebSocket passa pelo `reverse_proxy` sem configuração extra.

Limite de quadro WebSocket: **12 MiB** (pedido e resposta). O app deve comprimir imagens antes de `runs/start`. Acima disso o relay fecha o quadro.

Corpo JSON do plano de controle: até 64 KiB. Campo desconhecido é ignorado.

Todos os erros HTTP têm esta forma e `Content-Type: application/json`:

```json
{ "error": { "code": "unauthorized", "message": "credenciais inválidas" } }
```

Códigos de `error.code`:

| code | HTTP típico | Quando |
|---|---|---|
| `unauthorized` | 401 | login, token ausente, expirado ou revogado |
| `forbidden` | 403 | papel errado (membro criando conta, deviceId de outra conexão, role do hello não bate com o token) |
| `not_found` | 404 | IDE ou código de outra conta, ou inexistente |
| `invalid_request` | 400 | JSON, e-mail, senha ou chave pública inválidos |
| `conflict` | 409 | e-mail já cadastrado, código de vínculo já usado |
| `expired` | 410 | código de vínculo expirado |
| `rate_limited` | 429 | brute force, orçamento de login ou conexões demais. Header `Retry-After` em segundos |
| `encryption_required` | quadro WS | `rpc`, `rpc_result` ou `event` sem `enc: "naclbox"` |
| `e2e_keys_missing` | quadro WS | ainda falta a chave pública do outro lado |
| `decrypt_failed` | quadro WS | a IDE não abriu a caixa (não contém dados do bridge) |
| `key_mismatch` | quadro WS | a IDE apresentou outra chave pública que a gravada no vínculo |
| `internal` | 500 | falha interna; a mensagem não vaza SQL nem token |

`GET /api/v1/health` não exige autenticação:

```json
{ "ok": true, "service": "hivemind-relay", "version": "0.1.0", "time": "2026-11-19T12:00:00Z" }
```

---

## 3. Contas, senha e tokens

Não há cadastro público. Na primeira subida, se a tabela de contas está vazia, o processo cria um **owner**:

| Variável | Efeito |
|---|---|
| `HIVEMIND_SEED_EMAIL` | e-mail do owner. Se vazia: `owner@localhost` |
| `HIVEMIND_SEED_PASSWORD` | senha (10 a 200 caracteres). Se vazia, o processo gera 20 caracteres, imprime **uma vez** em stdout e grava `initial-password.txt` (modo `0600`) no diretório de dados |

Subidas seguintes **não** trocam a senha. Apague `initial-password.txt` depois de guardar a senha num gerenciador. O journal do systemd pode ter copiado a linha; troque a senha e, se quiser, `journalctl --rotate` / `vacuum`.

O owner cria outras contas (`role: "member"`). Membro não cria conta. Contas não se listam entre si.

Senha: argon2id, PHC `$argon2id$v=19$m=19456,t=2,p=1$…` (19 MiB, t=2, p=1). A verificação é serializada (um hash por vez) para a VM de 1/8 OCPU. E-mail inexistente também passa pelo argon2id, para não revelar cadastro pelo tempo de resposta.

### 3.1 Formato dos tokens

Segredo de 32 bytes, base64 sem padding (RFC 4648 §5), com prefixo fixo. Só o SHA-256 é gravado.

| Prefixo | Quem usa | Vida |
|---|---|---|
| `hmr_at_` | app e IDE no plano de controle (Bearer) | 15 minutos (`expiresIn` em segundos) |
| `hmr_rt_` | só `POST /auth/refresh` e logout | 30 dias, rotaciona a cada refresh |
| `hmr_dev_` | WebSocket da IDE | até revogar |

Header: `Authorization: Bearer hmr_at_…`

Reuso de um refresh já rotacionado **revoga a sessão inteira** (o refresh novo também deixa de valer). É o sinal de que o token vazou.

### 3.2 `POST /api/v1/auth/login`

```json
{ "email": "owner@exemplo", "password": "senha-inicial-ok" }
```

`200`:

```json
{
  "accessToken": "hmr_at_…",
  "refreshToken": "hmr_rt_…",
  "expiresIn": 900,
  "tokenType": "Bearer",
  "account": { "id": "acct_…", "email": "owner@exemplo", "role": "owner" }
}
```

`401` com a mesma mensagem para senha errada ou e-mail desconhecido. Depois de 5 falhas no mesmo IP **ou** no mesmo e-mail, em uma janela de 10 minutos, o par fica bloqueado por 15 minutos (`429`). Há também um teto de 10 tentativas de login por minuto por IP, aplicado **antes** do argon2id.

### 3.3 `POST /api/v1/auth/refresh`

```json
{ "refreshToken": "hmr_rt_…" }
```

`200` com o mesmo corpo do login (tokens novos). O refresh apresentado deixa de valer.

### 3.4 `POST /api/v1/auth/logout`

`Authorization: Bearer hmr_at_…` e/ou corpo `{ "refreshToken": "hmr_rt_…" }`. `204` sem corpo. A sessão deixa de aceitar o access token na hora, e o WebSocket do app aberto com ela é derrubado. **Não** revoga a credencial da IDE (`hmr_dev_`). Para desligar o PC, revogue a IDE.

### 3.5 `GET /api/v1/auth/me`

`200`: `{ "id", "email", "role" }`.

### 3.6 `POST /api/v1/auth/password`

```json
{ "currentPassword": "…", "newPassword": "…" }
```

Exige access token. `200` com tokens novos (login de novo). Todas as sessões anteriores da conta são revogadas.

### 3.7 `POST /api/v1/accounts` (só owner)

```json
{ "email": "outra@exemplo", "password": "senha-da-outra" }
```

`201`: `{ "id", "email", "role": "member" }`. `403` se quem chama não é owner. `409` se o e-mail existe.

---

## 4. IDEs (devices)

Uma conta pode ter várias IDEs (PC do trabalho, PC de casa). Cada uma tem id `dev_` + 16 bytes hex, nome (1–64 caracteres), chave pública X25519 da IDE, chave pública X25519 do app e a credencial `hmr_dev_`.

`online` é verdadeiro só enquanto o WebSocket daquela IDE está ligado. `e2eReady` é verdadeiro quando as duas chaves públicas de 32 bytes existem. `eventSeq` é o último evento durável.

### 4.1 `GET /api/v1/devices`

`200`:

```json
{
  "devices": [
    {
      "id": "dev_…",
      "name": "pc-trabalho",
      "online": true,
      "createdAt": "2026-11-19T12:00:00Z",
      "lastSeenAt": "2026-11-19T12:04:00Z",
      "idePublicKey": "<base64url 32 bytes>",
      "appPublicKey": "<base64url 32 bytes ou string vazia>",
      "e2eReady": true,
      "eventSeq": 41
    }
  ]
}
```

IDEs revogadas não aparecem. A lista é só da conta do token.

### 4.2 `POST /api/v1/devices` — IDE já logada

A IDE, com access token da conta, registra-se direto (o usuário digitou a senha no PC):

```json
{ "name": "pc-trabalho", "publicKey": "<chave pública da IDE>" }
```

`201`:

```json
{ "device": { "id": "dev_…", "name": "pc-trabalho", "e2eReady": false }, "deviceToken": "hmr_dev_…" }
```

`deviceToken` aparece **uma vez**. A IDE grava e usa no WebSocket. O app, depois, publica a chave dele (§4.4) ou manda `publicKey` no `hello`.

### 4.3 `GET /api/v1/devices/{id}` e `POST /api/v1/devices/{id}/revoke`

GET devolve o mesmo objeto de device. Revoke é `204` e derruba os sockets daquela IDE na hora. Outra conta recebe `404`.

### 4.4 `POST /api/v1/devices/{id}/peer-key`

O app (access token) instala ou **rotaciona** a chave pública do celular:

```json
{ "publicKey": "<32 bytes base64url>" }
```

`200` com o device atualizado. A IDE ligada recebe um quadro `peer_key` (§6). Quem tinha a chave antiga deixa de abrir as mensagens. A chave pública da IDE não se troca por este endpoint; perder a chave privada da IDE exige desvincular e vincular de novo.

Chaves aceitas em base64 padrão ou base64url, com ou sem `=`.

---

## 5. Vínculo por código / QR

Os dois lados precisam estar logados **na mesma conta**. O código tem 10 caracteres do alfabeto `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (sem 0/O/1/I), vale 10 minutos e é de uso único. O app pode exibir `ABCD-EFGH-JK`; o relay ignora espaços e hífens e compara em maiúsculas.

URI do QR:

```
hivemind://link?relay=https%3A%2F%2Frelay.exemplo&code=ABCDEFGHJK
```

`relay` é `HIVEMIND_RELAY_PUBLIC_URL` (sem barra final) ou, se ela não estiver definida, o scheme+host do pedido.

### 5.1 A IDE gera, o app confirma

1. IDE `POST /api/v1/link-codes` com access token:

```json
{ "role": "ide", "name": "pc-trabalho", "publicKey": "<chave da IDE>" }
```

`201`: `{ "code", "role": "ide", "name", "expiresAt", "uri", "idePublicKey", "appPublicKey" }`.

2. App `POST /api/v1/link-codes/{code}/confirm`:

```json
{ "publicKey": "<chave do app>" }
```

`200`: `{ "device": { … } }` **sem** `deviceToken`.

3. IDE `GET /api/v1/link-codes/{code}` até `status` deixar de ser `pending`:

| status | Significado |
|---|---|
| `pending` | esperando o outro lado |
| `ready` | IDE criada. Se a IDE iniciou, a **primeira** leitura pronta inclui `deviceToken` e `appPublicKey`. A seguinte já não traz o token |
| `expired` | passou de 10 minutos |

`404` se o código é de outra conta ou não existe. Tentativas falhas de confirm entram no mesmo bloqueio de brute force da conta.

### 5.2 O app gera, a IDE confirma

1. App `POST /api/v1/link-codes` com `{ "role": "app", "name": "pc-trabalho", "publicKey": "<chave do app>" }`.
2. IDE `POST /api/v1/link-codes/{code}/confirm` com `{ "publicKey": "<chave da IDE>", "name": "pc-casa" }`.
3. A resposta do confirm **inclui** `deviceToken` (quem confirma é a IDE). O GET seguinte não devolve o token.

O `deviceToken` nunca volta para o lado que não é a IDE.

---

## 6. WebSocket

`GET /api/v1/ws`.

Autenticação, nesta ordem:

1. `Authorization: Bearer <token>`
2. Se o header não vier, subprotocolo `bearer.<token>` (o servidor ecoa esse valor em `Sec-WebSocket-Protocol`). O token é base64url, então é um token de protocolo válido.

O primeiro quadro, em até 10 segundos, é `hello`. Sem isso a conexão cai.

Quadro (JSON, texto):

```json
{
  "v": 1,
  "id": "uuid-do-cliente",
  "type": "hello",
  "role": "app",
  "deviceId": "dev_…",
  "seq": 0,
  "enc": "",
  "payload": {}
}
```

`v` tem de ser `1`. `payload` é JSON cru (objeto), não uma string base64. `seq` só é significativo em `event` (o relay preenche). `enc` é `""` ou `"naclbox"`.

### 6.1 `hello` / `hello_ok`

Payload do hello (em claro):

```json
{ "resumeAfter": 40, "publicKey": "<chave pública de quem conecta>" }
```

- `role: "ide"` exige `hmr_dev_` daquele `deviceId`. Se `publicKey` vier, tem de ser a chave gravada no vínculo (`key_mismatch` caso contrário).
- `role: "app"` exige `hmr_at_` da conta dona da IDE. `publicKey` vazia mantém a chave já gravada; se vier diferente, **rotaciona** a chave do app e a IDE recebe `peer_key`.

`hello_ok` (em claro):

```json
{
  "v": 1,
  "id": "<o mesmo do hello>",
  "type": "hello_ok",
  "deviceId": "dev_…",
  "payload": {
    "seq": 41,
    "resumeFrom": 40,
    "peerPublicKey": "<chave pública do outro lado, ou vazia>",
    "e2eReady": true,
    "online": true
  }
}
```

Para o app, `online` significa que a IDE está com o socket ligado. `seq` é o último evento durável (0 se ainda não houve).

Em seguida, **só para o app**, o relay reenvia os eventos com `seq > resumeAfter` (no máximo 500, os mais antigos além da janela de 400 já foram apagados). A IDE não recebe o próprio histórico.

Se a IDE acabou de conectar, o relay entrega em seguida os RPCs cifrados que estavam na fila.

### 6.2 Tipos de quadro

| type | Quem envia | Cifrado | Efeito |
|---|---|---|---|
| `hello` / `hello_ok` | os dois / relay | não | handshake |
| `ping` / `pong` | relay a cada 25 s, ou qualquer lado | não | o `pong` repete o `id`. Sem quadro por 70 s a conexão cai |
| `ack` | app | não | aceito e ignorado. A retomada usa `resumeAfter`, não o ack |
| `peer_key` | relay | não | `{ "publicKey", "role" }` — a outra ponta publicou ou rotacionou a chave |
| `rpc` | app | sim | pedido do bridge |
| `rpc_result` | IDE | sim | resposta, mesmo `id` |
| `event` | IDE | sim | um evento ao vivo; o relay grava e põe `seq` |
| `error` | relay ou IDE | não | `{ "code", "message" }` sem conteúdo do bridge |

`rpc` sem `id` é `invalid_request`. Um segundo RPC com o mesmo `id` enquanto o primeiro está na fila é ignorado (`INSERT OR IGNORE`).

### 6.3 RPC = uma rota do bridge

Payload **depois de decifrar**:

```json
{
  "method": "POST",
  "path": "/api/v1/approvals/resolve",
  "query": "",
  "body": { "approvalId": "ap_1", "approved": true }
}
```

`query` é a query string sem `?` (`workspace=/abs/path`). `body` é JSON cru; omita em GET.

Rotas permitidas (qualquer outra, inclusive `GET /api/v1/events`, é recusada pela IDE com HTTP 400 e **não** é executada):

| Método | Caminho |
|---|---|
| GET | `/api/v1/health` |
| GET | `/api/v1/projects` |
| GET | `/api/v1/lanes` |
| GET | `/api/v1/sessions` |
| GET | `/api/v1/session` |
| POST | `/api/v1/runs/start` |
| POST | `/api/v1/runs/cancel` |
| POST | `/api/v1/runs/redirect` |
| POST | `/api/v1/approvals/resolve` |
| GET | `/api/v1/git/diffs` |
| GET | `/api/v1/git/prs` |
| GET | `/api/v1/git/issues` |
| GET | `/api/v1/autoreview/status` |
| POST | `/api/v1/autoreview/poll` |

O corpo e o significado de cada rota são os de `docs/SPEC_APP_MOBILE_NATIVO.md` §5. O app **não** abre `GET /api/v1/events` (SSE). O equivalente é o quadro `event`.

Resposta decifrada:

```json
{ "status": 200, "body": { "projects": [] } }
```

`status` é o HTTP que o bridge local devolveria. `body` é o JSON cru.

A IDE chama o handler do bridge **em processo**, com o token Bearer da LAN. Esse token não sai do PC e não aparece no relay.

### 6.4 Eventos

Payload decifrado = o mesmo JSON do SSE (`remote.StreamEvent`):

```json
{ "type": "lane_event", "data": { } }
```

`type` do **evento** (dentro da caixa) é `lane_event`, `autoreview_status`, `approval_resolved` ou o que o bridge já emite. Não confundir com o `type` do quadro, que é sempre `"event"`.

Se a IDE ficou sem relay por tempo bastante para o buffer local (512 eventos) transbordar, o primeiro evento depois da reconexão é `relay_gap`:

```json
{ "type": "relay_gap", "data": { "reason": "backlog_truncated", "lanes": [] } }
```

`lanes` é o snapshot vivo (`GET /api/v1/lanes`, todos os workspaces). Ao receber `relay_gap`, descarte a suposição de que o fluxo entre o último `seq` e este está completo: aplique `lanes` (aprovações pendentes inclusive) e, se precisar do texto que faltou, releia a sessão com `GET /api/v1/session`.

O relay atribui `seq` monotônico por IDE (começa em 1), grava o quadro já com `seq` e `enc`, e entrega a todos os apps daquela IDE. A janela guarda os últimos **400** eventos. Não dá para distinguir uma aprovação de outro evento, porque o corpo é opaco: a fila inteira é o que sobrevive à queda do app.

Retomada: ao reconectar, `resumeAfter` = último `seq` aplicado. O app também deve chamar `GET /api/v1/lanes` no início; as aprovações pendentes estão no snapshot mesmo se um evento antigo saiu da janela.

### 6.5 Fila de aprovações com a IDE fora do ar

`rpc` com a IDE desconectada (ou com o buffer cheio) fica no SQLite por até **3 minutos** e é entregue quando a IDE volta. A entrega é **pelo menos uma vez**: se a IDE cair depois de receber e antes do `rpc_result`, o mesmo `id` pode chegar de novo.

- `POST /api/v1/approvals/resolve` pode ser repetido. A segunda vez resolve `false` no bridge se o gate já não existe; o app confirma o estado por `GET /lanes` ou pelo evento `approval_resolved`.
- `POST /runs/start` **não** é idempotente. Se o `rpc_result` não chegar em ~30 s, leia `/lanes` antes de enviar de novo.

O access token dura 15 minutos. Quando faltar menos de 30 s, faça refresh e abra o WebSocket de novo. O `resumeAfter` cobre o buraco.

Reinício do **processo da IDE** perde aprovações que só existiam na memória do bridge (igual à LAN). Reinício do **relay** não perde a fila de eventos nem os RPCs ainda dentro dos 3 minutos.

### 6.6 Criptografia de ponta a ponta

Algoritmo: **NaCl box** (`crypto_box`: X25519 + XSalsa20-Poly1305), o de `golang.org/x/crypto/nacl/box` e de `tweetnacl` / `kotlin-multiplatform` equivalente (`crypto_box_seal` **não**; é o box com nonce explícito).

Cada lado gera um par X25519 de 32 bytes e guarda a privada só no aparelho. O relay só vê as públicas.

Para cifrar `plaintext`:

1. `mensagem = 0x01 || plaintext` (um byte de versão na frente).
2. `nonce` = 24 bytes de `crypto/rand`. Nunca reutilize o nonce com o mesmo par de chaves.
3. `ciphertext = box.Seal(mensagem, nonce, chavePúblicaDoOutro, chavePrivadaMinha)`.
4. Quadro:

```json
{
  "v": 1,
  "id": "rpc-1",
  "type": "rpc",
  "deviceId": "dev_…",
  "enc": "naclbox",
  "payload": {
    "nonce": "<base64url dos 24 bytes>",
    "ciphertext": "<base64url>"
  }
}
```

Para abrir: `box.Open(ciphertext, nonce, chavePúblicaDoOutro, chavePrivadaMinha)` e exija que o primeiro byte seja `0x01`. O segredo compartilhado é o DH das duas chaves; o remetente também consegue abrir o que ele mesmo cifrou. Um terceiro, não.

O relay recusa `rpc` / `rpc_result` / `event` sem as duas chaves (`e2e_keys_missing`) ou sem `enc: "naclbox"` (`encryption_required`). Ele confere que `nonce` e `ciphertext` são strings não vazias e **não decifra**. O SQLite guarda o quadro cifrado.

O que o relay ainda enxerga, por construção: tamanhos, horários, `type` do quadro, `deviceId`, `seq`, ids de RPC e as chaves públicas. Não enxerga objetivo, diff, token de LAN nem conteúdo de aprovação.

Enquanto `e2eReady` for falso, não mande RPC. Publique a chave (§4.4 ou no `hello`) e espere `hello_ok.e2eReady == true` ou um `peer_key`.

### 6.7 Receita mínima do app

1. `POST /auth/login`. Guarde o refresh num cofre (Keystore). Não guarde a senha.
2. `GET /devices`. Mostre nome, `online` e deixe o usuário escolher.
3. Se `appPublicKey` estiver vazia ou for de outra instalação, gere um par novo e `POST /devices/{id}/peer-key`.
4. Abra `wss://…/api/v1/ws` com o access token. `hello` com `role: "app"`, o `deviceId` escolhido, `resumeAfter` e a `publicKey`.
5. Para cada ação que hoje seria HTTP no bridge, monte o `RPCRequest`, cifre para `idePublicKey` e envie `rpc`. Case o `rpc_result` pelo `id`.
6. Aplique `event` em ordem de `seq`. Ignore `seq` já visto.
7. Antes do access token expirar, `refresh` e reconecte com o último `seq`.
8. Se a IDE estiver `online: false`, ainda pode enviar `approvals/resolve`: fica na fila por até 3 minutos. Para o resto, mostre a IDE offline.
9. Logout: `POST /auth/logout` e apague os tokens de sessão. Isso não desliga a IDE; a lista de devices continua válida no próximo login.

---

## 7. Lado da IDE (já neste repositório)

No modal **App Mobile** há a seção **Acesso remoto**: URL `https`, e-mail, senha (não é gravada), ligar/desligar o túnel, gerar código/QR, confirmar código do celular, desvincular e sair.

Arquivo `relay.json` ao lado de `remote-bridge.json` (`%LOCALAPPDATA%\HiveMind` ou o diretório de config do SO), modo `0600`. Contém refresh token, `hmr_dev_`, par de chaves da IDE e a chave pública do app. A LAN (`remote-bridge.json`, QR `hivemind://pair`) continua independente: dá para usar só LAN, só relay, ou os dois.

O chip da barra mostra `mobile: on`, `mobile: relay`, `mobile: on+relay` ou `mobile: off`.

---

## 8. O que ficou de propósito para depois

- Cliente Android (outro repositório), implementado a partir desta spec.
- Recuperação de senha por e-mail: não há SMTP. Decisão do dono se quer isso ou se a troca em `/auth/password` + acesso SSH bastam.
- Cadastro aberto na internet: desligado. Só o owner cria membros.
- Notificação push com o app em background (o evento só chega com o WebSocket ligado; em background o app precisa de um canal à parte).
- Subir o limite de 12 MiB se uma foto não couber mesmo comprimida (`o limite está fixo no binário desta versão`).
- O relay não esconde metadados (tamanho e timing). Quem precisar disso além do NaCl box trata numa versão futura.

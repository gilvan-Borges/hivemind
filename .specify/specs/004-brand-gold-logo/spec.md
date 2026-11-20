# Spec 004 — Destaque dourado e logo HiveMind

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1, D3, D4, D6 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
Os tokens `--accent*` já são o dourado Havrus (`#C6A15B` / `#E8C881`), mas destaque de seleção, brilho, SVG e badges ainda usam violeta solto. A logo da IDE, o favicon, o ícone do executável e o instalador ainda apontam para o símbolo antigo. A logo nova está em `assets/brand/`.

## 2. Escopo e Não-Escopo
- **Inclui:** trocar violeta de destaque por `--accent*`; ícone quadrado em `ide/build/appicon.png`, `ide/build/windows/icon.ico`, favicon, titlebar, tela inicial e estados vazios; wordmark horizontal no topo do README; instalador Inno Setup; apagar a logo antiga.
- **Não inclui:** lógica de orquestração, temas nomeados da TUI além do matiz violeta, paleta de sinais (sucesso, perigo, aviso, info) e um diálogo Sobre novo (a IDE não tem essa tela).

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Texto e ícone de destaque | 2 | `--accent` | `BrandGold` |
| Hover, foco, texto claro | 2 | `--accent-strong` | `BrandGoldStrong` |
| Fundo de seleção | 1–2 | `--accent-subtle` | `BrandGoldSubtle` |
| Borda de seleção | 2 | `--accent-border` | `BrandGoldBorder` |
| Logo | 0–1 | imagem `assets/brand/hivemind-icon.png` | fora deste repositório |

## 4. Estados Obrigatórios (D5)
- **Carregando:** sem mudança de layout; a logo é estática.
- **Erro:** sinais existentes (`--color-danger*`) permanecem.
- **Vazio:** tela inicial e chat vazio mostram o ícone novo.
- **Dados:** titlebar mantém o ícone ao lado do nome.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--accent` (`#C6A15B`) | `--surface-card` (`#111111`) | >= 7:1 |
| `--accent-strong` (`#E8C881`) | `--surface-card` (`#111111`) | >= 10:1 |
| `--accent-strong` | `--surface-page` (`#050505`) | >= 10:1 |

Texto que era violeta claro passa a `--accent-strong`. `--accent-muted` não entra em texto.

## 6. Critérios de Aceite
- [ ] Zero cores literais violeta nos destaques da IDE; hex novo só em `:root`.
- [ ] `npm run check:design` sem falhas.
- [ ] `npm test` e `npm run build` em `ide/frontend`.
- [ ] `go build ./...` e `go test ./...` em `ide/`.
- [ ] `.ico` com 16, 24, 32, 48, 64, 128 e 256. Logo antiga removida.

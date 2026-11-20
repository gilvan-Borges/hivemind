# Plan de Design — 002-icons-dedupe

## Abordagem

1. Auditar colisões em `icons.ts` (mesmo `ms("...")` em papéis distintos).
2. Remapear para glifos Material Symbols válidos e semanticamente distintos.
3. Na activity bar, preferir outline (`fill=false`) para aproximar o traço do Lucide do IPG sem trocar de família.
4. Validar tokens + build; não commit/push/PR nesta sessão Cursor.

## Mapa de remapeamento

| Chave | Antes | Depois | Motivo |
|---|---|---|---|
| `swarm` | `hub` (fill) | `hub` (outline) | Mantém grafo multiagente; outline |
| `mcp` | `hub` | `dns` | Servidores MCP ≠ hub de agentes |
| `sparkle` | `auto_awesome` | `auto_awesome` | Residente / “sparkle” |
| `aiUsage` | `auto_awesome` | `monitoring` | Uso & limites / telemetria |
| `tokens` | `bolt` | `generating_tokens` | Consumo de tokens ≠ ação bolt |
| `bolt` | `bolt` | `bolt` | Mantém |
| activity bar filled | vários fill | outline | Visual mais próximo do Lucide |

---
name: revisar-e-testar
description: >
  Use when reviewing a Pull Request or code change, when the user asks to review
  or test a change, or before approving a PR. Detects the project stack touched
  by the diff, runs the targeted test suite on the PR checkout/worktree, and
  publishes a clean, senior-level engineering review without internal bot headers.
---

# Revisar e testar

Use ao revisar um Pull Request, ao concluir uma mudança de código ou quando pedirem `revisa`, `review` ou `testa isso`.
Revisão séria de código executa os testes reais dos módulos alterados no checkout do PR e publica um parecer técnico limpo e profissional.

## 1. Identifique a stack real pelo diff (qualquer linguagem/repositório)

Inspecione os arquivos alterados no PR e identifique os módulos/serviços tocados conforme o próprio repositório analisado:

| Arquivos alterados no diff | Comando alvo sugerido (`run_shell`) |
|---|---|
| Java / Kotlin (`pom.xml`) | `mvn -B -q -Dstyle.color=never test -Dtest=<TestesAfetados>` ou `mvn -B -q -Dstyle.color=never -pl <modulo> test` |
| Java / Kotlin / Android (`build.gradle*`) | `./gradlew --no-daemon :modulo:test` (ou `testDebugUnitTest` em Android) |
| Go (`go.mod`, `*.go`) | `go test -count=1 ./caminho/do/pacote/...` |
| Node / TypeScript (`package.json`, `*.ts`, `*.tsx`, `*.js`) | `npm test` (ou `pnpm test` / `npx vitest run <arquivo>`) no pacote tocado |
| Python (`pyproject.toml`, `*.py`) | `uv run pytest -q <caminho_dos_testes>` (ou `.venv/Scripts/pytest -q` / `pytest -q <caminho_dos_testes>`) |
| Rust (`Cargo.toml`, `*.rs`) | `cargo test -p <crate>` |
| .NET (`*.csproj`, `*.sln`, `*.cs`) | `dotnet test <projeto>` |
| Apenas documentação / specs (`*.md`, diagramas, texto) | Não rode suíte de código; a consistência técnica do documento é a revisão |

- Rode apenas os testes dos módulos/serviços efetivamente alterados pelo diff (não rode o monorepo inteiro sem necessidade).
- No Windows / PowerShell, execute um comando por chamada (ou encadeie com `;`).
- **Runtime/ferramenta ausente (ex.: Python):** em projetos Python, tente `uv run pytest` ou `.venv` local primeiro; **nunca** faça varredura recursiva no disco (`dir /s`, `find /`) procurando `python`. Se o runtime/ferramenta não existir no ambiente, pare e **peça permissão ao usuário antes de instalar**.

### Mudanças de Frontend / Visual (proporcional ao impacto)

Antes de testar um PR ou pedido avulso que toque arquivos de frontend/UI (`*.tsx`, `*.jsx`, `*.css`, `*.scss`, `*.html`, `*.vue`, `*.svelte`, `*.dart` de UI/widgets, `*.kt` Compose), avalie se houve **impacto visual** e qual o **porte da mudança**:

1. **Sem impacto visual** (lógica pura, hooks de dados sem renderização, tipos, backend, docs): siga apenas a tabela acima, sem abrir browser nem rodar suíte visual.
2. **Mudança visual pontual / pequena** (ajuste de texto, 1–2 componentes isolados, correção pontual de classe/token): rode os testes unitários/de componente do pacote afetado, validação de design tokens (`npm run check:design` / `check-design-tokens.mjs` se existir) e `npm run build` (ou `flutter test <arquivo>`). **Não** abra browser nem tire screenshots.
3. **Mudança visual grande / estrutural** (nova tela/página/fluxo, refactor de layout/shell/navegação, alteração em CSS global/tokens de tema, múltiplos componentes/telas ou ajustes responsivos amplos):
   - Rode a **suíte completa** de testes e build do módulo de frontend afetado (ex.: `npm test && npm run build`, `npm run check:design`, ou `flutter test` completo do módulo/app; mais testes E2E se existirem no repo).
   - Se houver ambiente renderizável local e ferramenta de browser disponível (`playwright` via `mcp_enable`, `chrome-devtools` ou `hivemind-browser`), abra a rota/tela alterada e inspecione primeiro via `browser_snapshot` (econômico em tokens) e erros de console/layout.
   - **Tire screenshot (`browser_take_screenshot` / captura) somente do que ficar ruim ou suspeito** (quebra de layout, overflow, sobreposição, contraste ilegível, desalinhamento ou erro visual) — nunca tire prints em série de telas saudáveis para não desperdiçar tempo e tokens.
   - Se for **revisão de PR**, reporte objetivamente o defeito visual encontrado (rota/viewport/elemento) e use `event=REQUEST_CHANGES` (sem expor caminhos temporários ou cabeçalhos robóticos). Se for **pedido de implementação**, corrija o defeito visual antes de concluir.
   - Se o ambiente não permitir renderizar a tela ao vivo (ex.: falta de backend/credencial externa no checkout), registre o que foi validado via testes completos + build sem inventar inspeção visual.

## 2. Checkout do PR e execução obrigatória dos testes

- Em revisões de PR, o HiveMind já prepara automaticamente uma **worktree efêmera destacada** com o commit exato do PR (`pull/<N>/head`) como diretório de trabalho das ferramentas.
- **NUNCA** deixe de testar nem escreva no GitHub que *"a branch não consta no clone local"* ou que *"a worktree está em outra branch"*:
  - Se precisar validar no diretório atual, os arquivos do commit do PR já estão materializados na worktree efêmera;
  - Se por qualquer motivo estiver fora do commit do PR, busque o commit real com `git fetch --no-tags origin pull/<N>/head` — jamais publique desculpa de branch ausente em um parecer de PR.
- Se um teste falhar no código do PR, reporte o comando executado e o erro real de forma objetiva e use `event=REQUEST_CHANGES`.

## 3. Formato profissional do parecer no GitHub (`github_review_pr`)

O comentário publicado no PR é lido por toda a equipe de engenharia. **NUNCA** publique cabeçalhos robóticos ou checklists internas de ferramenta, tais como:
- ❌ Proibido escrever `Superfícies: ...` ou `Não é: IDE, CLI, mobile, extensão`!
- ❌ Proibido escrever `⚠️ ... — NÃO TESTADO — a branch ... não consta no clone local`!

Estruture o parecer como um **Engenheiro Sênior / Tech Lead humano**:
1. **Parecer técnico direto:** avalie de forma clara e objetiva a correção das regras de negócio, contratos, segurança, tratamento de erros e aderência à arquitetura do projeto (respondendo também aos pontos levantados por outros revisores, se houver).
2. **Validação executada:** inclua uma linha discreta e objetiva com os testes realmente rodados e o resultado (ex.: ``**Validação:** `mvn -B -pl modulo-financeiro test -Dtest=MinhaGuardaTest` executado com sucesso.``). Em PRs exclusivos de documentação (`.md`/specs), mencione apenas a consistência documental.
3. **Pontos de ajuste (se houver):** liste `arquivo:linha`, causa raiz e sugestão objetiva apenas quando existir problema real.

## 4. Veredito formal no GitHub

- Código correto + testes dos módulos alterados passando: publique `github_review_pr` com `event=APPROVE`.
- Diff exclusivamente de documentação/spec (`.md`) consistente: publique `github_review_pr` com `event=APPROVE` direto (sem suíte e sem avisos de não testado).
- Defeito funcional, quebra de contrato, falha de segurança ou teste quebrando: `event=REQUEST_CHANGES`.
- Pedido explícito de apenas comentar ou modo somente-comentário: `event=COMMENT`.

## 5. Pós-merge: atualizar a task no Jira

- Sempre que realizar o merge de um PR (`github_merge_pr`) e o título, descrição, branch ou contexto da conversa referenciar uma task do Jira (ex.: `PROJ-123`):
  1. Passe `jira_key` ao chamar `github_merge_pr` (a ferramenta atualiza automaticamente a issue para **Concluído/Done** quando o Jira estiver conectado).
  2. Confira no retorno do `github_merge_pr` se a task foi movida para **Concluído**. Se houver aviso ou se a atualização automática não tiver ocorrido, chame `jira_transition` para mover a issue para **Concluído** (e registre o link do PR com `jira_comment` se aplicável).
  3. Informe no resumo final ao usuário tanto o merge do PR quanto a atualização do status da task no Jira.

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
| Java / Kotlin (`pom.xml`) | `mvn -B test -Dtest=<TestesAfetados>` ou `mvn -B -pl <modulo> test` |
| Java / Kotlin / Android (`build.gradle*`) | `./gradlew --no-daemon :modulo:test` (ou `testDebugUnitTest` em Android) |
| Go (`go.mod`, `*.go`) | `go test -count=1 ./caminho/do/pacote/...` (em `ide/frontend/**`: `npm test` + `node scripts/check-design-tokens.mjs` se tocar CSS) |
| Node / TypeScript (`package.json`, `*.ts`, `*.tsx`, `*.js`) | `npm test` (ou `pnpm test` / `npx vitest run <arquivo>`) no pacote tocado |
| Python (`pyproject.toml`, `*.py`) | `pytest <caminho_dos_testes>` |
| Rust (`Cargo.toml`, `*.rs`) | `cargo test -p <crate>` |
| .NET (`*.csproj`, `*.sln`, `*.cs`) | `dotnet test <projeto>` |
| Apenas documentação / specs (`*.md`, diagramas, texto) | Não rode suíte de código; a consistência técnica do documento é a revisão |

- Rode apenas os testes dos módulos/serviços efetivamente alterados pelo diff.
- No Windows / PowerShell, execute um comando por chamada (ou encadeie com `;`).

## 2. Checkout do PR e execução obrigatória dos testes

- Em revisões de PR, o HiveMind já prepara automaticamente uma **worktree efêmera destacada** com o commit exato do PR (`pull/<N>/head`) como diretório de trabalho das ferramentas.
- **NUNCA** deixe de testar nem escreva no GitHub que *"a branch não consta no clone local"* ou que *"a worktree está em outra branch"*. Se por qualquer motivo estiver fora do commit do PR, busque o commit real com `git fetch --no-tags origin pull/<N>/head`.
- Se um teste falhar no código do PR, reporte o comando executado e o erro real de forma objetiva e use `event=REQUEST_CHANGES`.

## 3. Formato profissional do parecer no GitHub (`github_review_pr`)

O comentário publicado no PR é lido por toda a equipe de engenharia. **NUNCA** publique cabeçalhos robóticos ou checklists internas de ferramenta (`Superfícies: ...`, `Não é: IDE, CLI, mobile, extensão`, ou avisos de `branch não consta no clone local`).

Estruture o parecer como um **Engenheiro Sênior / Tech Lead humano**:
1. **Parecer técnico direto:** correção das regras de negócio, contratos, segurança, tratamento de erros e aderência arquitetural.
2. **Validação executada:** linha objetiva com os testes realmente rodados e o resultado. Em PRs exclusivos de documentação (`.md`/specs), mencione apenas a consistência documental.
3. **Pontos de ajuste (se houver):** `arquivo:linha`, causa raiz e sugestão objetiva apenas quando existir problema real.

## 4. Veredito formal no GitHub

- Código correto + testes dos módulos alterados passando: `github_review_pr` com `event=APPROVE`.
- Diff exclusivamente de documentação/spec (`.md`) consistente: `github_review_pr` com `event=APPROVE` direto.
- Defeito funcional, quebra de contrato, falha de segurança ou teste quebrando: `event=REQUEST_CHANGES`.
- Pedido explícito de apenas comentar ou modo somente-comentário: `event=COMMENT`.

## 5. Pós-merge: atualizar a task no Jira

- Sempre que realizar o merge de um PR (`github_merge_pr`) e o título, descrição, branch ou contexto da conversa referenciar uma task do Jira (ex.: `PROJ-123`):
  1. Passe `jira_key` ao chamar `github_merge_pr` (a ferramenta atualiza automaticamente a issue para **Concluído/Done** quando o Jira estiver conectado).
  2. Confira no retorno do `github_merge_pr` se a task foi movida para **Concluído**. Se houver aviso ou se a atualização automática não tiver ocorrido, chame `jira_transition` para mover a issue para **Concluído** (e registre o link do PR com `jira_comment` se aplicável).
  3. Informe no resumo final ao usuário tanto o merge do PR quanto a atualização do status da task no Jira.

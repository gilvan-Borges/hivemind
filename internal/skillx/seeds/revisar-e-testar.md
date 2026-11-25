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
| Go (`go.mod`, `*.go`) | `go test -count=1 ./caminho/do/pacote/...` |
| Node / TypeScript (`package.json`, `*.ts`, `*.tsx`, `*.js`) | `npm test` (ou `pnpm test` / `npx vitest run <arquivo>`) no pacote tocado |
| Python (`pyproject.toml`, `*.py`) | `pytest <caminho_dos_testes>` |
| Rust (`Cargo.toml`, `*.rs`) | `cargo test -p <crate>` |
| .NET (`*.csproj`, `*.sln`, `*.cs`) | `dotnet test <projeto>` |
| Apenas documentação / specs (`*.md`, diagramas, texto) | Não rode suíte de código; a consistência técnica do documento é a revisão |

- Rode apenas os testes dos módulos/serviços efetivamente alterados pelo diff (não rode o monorepo inteiro sem necessidade).
- No Windows / PowerShell, execute um comando por chamada (ou encadeie com `;`).

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

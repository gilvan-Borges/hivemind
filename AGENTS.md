# Diretrizes para Agentes & Contribuidores — HiveMind

## Política de Repositório Público (OBRIGATÓRIA)

Este repositório (`gilvan-Borges/HiveMind`) e o companion (`HiveMind-App`) são **repositórios públicos no GitHub**. Toda alteração de código, teste, fixture, comentário, spec, documentação ou commit **DEVE** obedecer às regras abaixo:

1. **Zero segredos ou arquivos sensíveis:** nunca commitar chaves de API, tokens (`sk-`, `ghp_`, `AIza`, `ATATT`, `ya29.`, `xoxb-`, etc.), senhas, arquivos `.env`, chaves privadas (`.pem`, `.key`, `.jks`, `.keystore`), bancos locais (`.sqlite`, `.db`) ou arquivos de credenciais (`credentials.json`, `api-keys.json`, `oauth-session.json`, `provider-profiles.json`, `relay.json`, `local.properties`).
2. **Zero binários compilados no Git:** nunca versionar instaladores (`.exe`, `.msi`), APKs/AABs ou binários de build — distribua artefatos exclusivamente via **GitHub Releases**.
3. **Zero dados reais de terceiros, empresas ou clientes:**
   - **Proibido** incluir em código, testes, comentários, placeholders ou documentação: nomes de clientes/empresas privadas, domínios corporativos reais (`*.atlassian.net`, remotes privados), chaves reais de issues/PRs de projetos fechados, caminhos locais de pastas privadas (`C:\Projetos...`), usernames GitHub de colegas/terceiros ou e-mails pessoais/corporativos reais.
   - **Única identificação pessoal permitida:** os dados do autor/mantenedor do projeto (**Gilvan Borges** / `gilvan-Borges`) em `go.mod`, metadados de build/instalador, autoria de commits e licença.
4. **Use sempre dados sintéticos em exemplos e testes:** utilize exclusivamente organizações, projetos e identidades fictícias (`acme`, `org-alpha`, `org-beta`, `example.com`, `PROJ-123`, `app-mobile`, `@reviewer`, `conta1@example.com`, `sk-test-fake-0000`).
5. **Heurísticas sempre genéricas:** nunca codifique prefixos, sufixos ou nomes de projetos de clientes específicos em lógica de negócio (ex.: detecção de projeto Jira, matching de orgs ou filtros de PR).

## Design System & SDD
- Para alterações visuais na IDE (`ide/frontend/`) ou no `HiveMind-App`, siga obrigatoriamente `.specify/memory/constitution.md` (Artigos `D1–D9`) e rode `check-design-tokens`.

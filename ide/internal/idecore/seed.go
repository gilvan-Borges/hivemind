package idecore

import (
	"fmt"
	"os"
	"path/filepath"
)

// seedNativeSkills são as skills NATIVAS semeadas automaticamente no catálogo
// da IDE (%LOCALAPPDATA%\HiveMind\skills) no primeiro startup. Elas suportam os
// modos Planejar · Feature (plan) e Planejar · Projeto SDD (plan:sdd / plan:sdd-jira).
// Uma skill já existente NUNCA é sobrescrita: edições do usuário no painel de
// Skills prevalecem sempre.
var seedNativeSkills = map[string]string{

	"plan-feature-architect": `# Plan Feature Architect (Chefe)

Planejamento técnico de features e tasks grandes em projetos JÁ EXISTENTES, antes de disparar o Squad. Use quando o objetivo envolver criar, editar ou refatorar código em base existente.

## Passo a passo
1. **Recon do terreno**: inspecione com read_file/list_dir/git_status/git_diff os arquivos e módulos que o objetivo toca; identifique convenções, camadas e padrões do projeto (respeite-os).
2. **Mapa de impacto**: liste arquivos a criar/editar/excluir por módulo, e o que NÃO deve ser tocado (código estável, contratos públicos).
3. **Riscos e testes**: aponte riscos técnicos (compatibilidade, migração, performance, segurança) e defina quais testes protegem a mudança (unitários, integração, contratos).
4. **Fatiamento paralelo sem conflito**: divida o trabalho em dev_tasks com fronteira de ARQUIPOS disjunta (cada arquivo pertence a no máximo 1 dev). 1 dev para tarefas focadas; 2 a N para módulos independentes. Cada item deve ser auto-contado: objetivo, arquivos, critérios de pronto.
5. **Delegate**: chame delegate_to_squad com "plan" (mapa de impacto + riscos + testes) e "dev_tasks" (fatias). Passe em "skills"/"native_skills" apenas as diretrizes realmente relevantes.

## Regras
- Nunca proponha editar arquivos fora do escopo do pedido.
- Contratos públicos (API, eventos, schemas) só mudam com justificativa explícita no plano.
- O plano é para engenheiro experiente: direto, sem redundância, com trade-offs quando houver alternativas.
`,

	"sdd-discovery": `# SDD Discovery (Chefe)

Entrevista guiada para iniciar um projeto do ZERO no modo Planejar · Projeto SDD. Objetivo: zerar ambiguidades materiais antes de qualquer documentação. NÃO gera código, NÃO cria issues.

## Rodadas de entrevista (máx. 5-8 perguntas objetivas por rodada)
1. **Negócio**: qual problema real; quem paga a conta; métrica de sucesso; o que acontece se não for feito.
2. **Usuários**: personas, jornada principal, volume esperado (usuários/dia, req/s, dados).
3. **Requisitos funcionais**: capacidades nucleares (MVP) vs. adiáveis; prioridade relativa.
4. **Requisitos não funcionais**: latência, throughput, consistência (forte/eventual), disponibilidade, custo, segurança/PII, observabilidade.
5. **Stack e restrições**: linguagens preferidas, persistência, mensageria, cloud/on-prem, tempo, orçamento, skills do time.
6. **Integrações**: APIs externas, eventos, sistemas legados, contratos já fixos.
7. **Regras de domínio**: invariantes, cálculos, permissões, comply/regulatório.
8. **Deploy e operação**: ambientes, CI/CD, migrações, backup/restore.

## Regras
- Pergunte APENAS o que muda decisão de arquitetura; detalhes cosméticos ficam como premissa registrada.
- Resposta vaga ou "tanto faz": registre a premissa explicitamente e siga (decisão reversível).
- Quando o usuário pedir para avançar, encerre a entrevista e passe para sdd-architecture-docs (Etapa 2).
- Nunca escreva docs antes do escopo confirmado; nunca escreva código nesta fase.
`,

	"sdd-architecture-docs": `# SDD Architecture & Docs (Chefe)

Gera a documentação completa de Spec-Driven Development na fase de docs (após o Discovery), usando APENAS write_doc (.md em docs/, specs/, .specify/, README.md). Código permanece bloqueado até os gates serem aprovados.

## Artefatos obrigatórios (layout padrão HiveMind/ipg)
1. **specs/01-spec.md** — visão do produto; escopo; NÃO-escopo; personas; requisitos funcionais numerados (RF-xx); requisitos não funcionais (RNF-xx); critérios de aceite testáveis por requisito.
2. **specs/02-arquitetura.md** — estilo arquitetural (monólito modular primeiro, microsserviços só com razão concreta); componentes e fronteiras; modelo de dados (entidades, chaves, índices); fluxos principais (sequência); segurança (autenticação, autorização, PII); observabilidade (logs estruturados, métricas, traces).
3. **docs/adr/NNNN-titulo.md** — um ADR por decisão relevante (stack, persistência, mensageria, consistência): Contexto → Decisão → Consequências (curto e objetivo).
4. **specs/03-contratos.md** — contratos de API (recursos, verbos, payloads, códigos), eventos (tópico, payload, consumidor), schemas de dados; versionamento e regras de evolução.
5. **specs/04-plano.md** — ondas de entrega (Onda 1 = esqueleto vertical walking skeleton; ondas seguintes por valor/dependência); dependências entre ondas; riscos top 5 com mitigação.
6. **specs/05-tarefas.md** — backlog: Epics → Tasks → Subtasks; cada Task referencia RF/RNF, lista arquivos previstos e critérios de aceite copiados da spec; marcação da onda de cada item.

## Regras de qualidade
- Critério de aceite sem ser verificável por teste não é critério de aceite — reescreva.
- Projete para falha: timeouts, retries com backoff+jitter, circuit breaker, idempotência.
- Consistência explícita: onde é forte, onde é eventual (outbox/saga quando necessário).
- Simplicidade primeiro: nenhuma abstração sem uso concreto.
- Ao concluir, chame sdd_docs_ready com o resumo executivo e PARE (Gate 1).
`,

	"sdd-jira-planner": `# SDD Jira Planner (Chefe)

Cria o backlog Jira espelhando a spec SDD aprovada (Gate 1) na fase plan:sdd-jira. Nenhum desenvolvimento começa antes deste backlog existir e o Gate 2 ser aprovado.

## Passo a passo
1. Leia specs/05-tarefas.md (fonte da verdade) e specs/04-plano.md (ondas) com read_file.
2. **Epics**: um Epic por épico/onda de entrega via jira_create_issue (type=Epic); description com objetivo da onda + referência à spec.
3. **Tasks/Stories**: crie sob o Epic (parent=chave do Epic), na ordem das ondas; description DEVE conter os critérios de aceite da spec (copie, não parafrase) e os arquivos previstos.
4. **Subtasks**: jira_create_subtask apenas para quebras finas de execução (setup, testes, migração).
5. **Rastreabilidade**: atualize specs/05-tarefas.md com write_doc gravando o mapeamento tarefa→chave Jira (PROJ-xxx) ao lado de cada item.
6. **Disparo**: chame delegate_to_squad com "plan" (ondas resumidas de specs/04-plano.md) e "dev_tasks" (1 item por onda/desenvolvedor paralelo, frontes de arquivo disjuntas). PARE para o Gate 2.

## Regras
- Título curto e actionável; details ricas vão na description.
- Não invente chaves: se o Jira não estiver configurado, informe o que falta (Conector Jira Nativo + jira_enabled) e pare.
- Tasks da mesma onda devem ser executáveis em paralelo sem conflito de arquivos.
`,

	"sdd-dev-executor": `# SDD Dev Executor (Dev)

Implementação fiel aos contratos durante o desenvolvimento SDD (pós Gate 2). A spec é o contrato: implemente exatamente o que ela define.

## Passo a passo
1. Antes de codar, leia (read_file): specs/01-spec.md (requisitos da sua fatia), specs/02-arquitetura.md, specs/03-contratos.md, docs/adr/ e a sua subtarefa do plano do Chefe.
2. Siga o ADR correspondente à sua parte sem relitigar decisões já aprovadas; discordância técnica → registre no relatório, não mude o contrato por conta própria.
3. Respeite frontes de arquivos da sua subtarefa (worktree isolada): NÃO edite arquivos de outra fatia; contratos compartilhados já estão nos docs.
4. Implemente + testes na mesma entrega: unitários para regra de negócio; integração para dependências reais (Testcontainers quando houver container no projeto); erros tratados, sem falha silenciosa.
5. Estilo do projeto prevalece: nomes, camadas, padrões e lint existentes.
6. Reporte: o que implementou, testes rodados (comando + resultado), o que ficou de fora e por quê.

## Regras
- NUNCA git commit/push/PR (exclusivo do Chefe/Arquiteto).
- API não confiável (LLM/terceiros): valide saída com schema/estrutura; trate como entrada hostil.
- Divergência encontrada na spec (bug, ambiguidade, impossibilidade): implemente o mínimo consistente e sinalize no relatório para o QA/Chefe decidir.
`,

	"sdd-qa-verifier": `# SDD QA Verifier (QA)

Validação ponto a ponto da entrega SDD contra a spec: cada critério de aceite de specs/01-spec.md (e das tasks em specs/05-tarefas.md) precisa ser verificado.

## Passo a passo
1. Leia specs/01-spec.md (critérios de aceite), specs/03-contratos.md (contratos) e o relatório dos devs.
2. **Checklist por critério**: para cada critério de aceite da onda entregue, verifique com execução real (run_shell: build, testes, smoke da API/serviço) — não confirme por leitura de código apenas.
3. **Contratos**: confirme endpoints/eventos/schemas implementados conforme specs/03-contratos.md (verbos, payloads, códigos de status).
4. **Não funcionais básicos**: build limpo, testes verdes, erros tratados, sem segredos hardcoded, logs sem PII.
5. **Veredito**: tudo verde → qa_approve com resumo do que foi verificado; qualquer falha → qa_request_changes apontando critério de aceite específico, como reproduzir e como corrigir.

## Regras
- Máximo 3 rodadas de correção: após a 3ª divergência material contra a spec, sinalize no relatório que a spec precisa de revisão do Chefe.
- Seja específico: "falhou porque X, critério RF-12, evidência Y".
- Não aprove "no geral está bom": aprovação é por critério de aceite.
`,
}

// SeedNativeSkills grava as skills nativas padrão no catálogo da IDE no primeiro
// uso, sem NUNCA sobrescrever edições do usuário (skill existente é preservada).
func SeedNativeSkills() (seeded []string, err error) {
	dir := NativeSkillsDir()
	for name, body := range seedNativeSkills {
		p := filepath.Join(dir, name, "SKILL.md")
		if _, statErr := os.Stat(p); statErr == nil {
			continue // preserva edição do usuário
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, fmt.Errorf("semear skill nativa %s: %w", name, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("semear skill nativa %s: %w", name, err)
		}
		seeded = append(seeded, name)
	}
	return seeded, nil
}

package team

import "fmt"

const defaultModel = "gemini-flash-latest"

// DefaultInstructions is the Portuguese system prompt for a role.
func DefaultInstructions(role string) string {
	switch NormalizeRole(role) {
	case RoleProjectManager:
		return `Você é o gerente de projeto (Chefe) do time HiveMind. Responda em português.
Leia o objetivo, PRs e o contexto Jira quando existirem. Produza um plano curto: escopo, critérios de aceite e como o trabalho será dividido entre os desenvolvedores em worktree separada.
Governança Git/PR: apenas você (Chefe) ou o Arquiteto podem realizar commits, push, abrir PR, responder revisões em PR de própria autoria, re-solicitar revisão e mesclar PRs (após aprovação do revisor: automático em homolog e mediante permissão do usuário em main).
Se as ferramentas Jira estiverem disponíveis e houver uma chave de issue, crie subtarefas para o desenvolvimento e para o QA e comente o progresso.
Não edite código-fonte diretamente. Não invente que os testes passaram.
Quando o usuário pedir para executar um comando, use run_shell: é o terminal desta sessão. Não diga que o terminal não existe. Comandos perigosos só rodam depois da aprovação do usuário.`
	case RoleTechLead:
		return `Você é o tech lead do time HiveMind. Responda em português.
Transforme o plano do gerente em tarefas concretas, uma por desenvolvedor, com os arquivos que cada um pode alterar. Os desenvolvedores trabalham em paralelo em worktrees isoladas e não enxergam o trabalho uns dos outros, então as tarefas não podem disputar o mesmo arquivo.
Aponte o comando de teste que o QA deve rodar.`
	case RoleArchitect:
		return `Você é o arquiteto do time HiveMind. Responda em português.
Defina a abordagem técnica mínima para cumprir o objetivo sem refatorar o que não foi pedido. Cite caminhos de arquivo e contratos. Para alterar arquivos existentes, use sempre edit_file (preserva encoding e quebras de linha); use write_file apenas para arquivos novos.
Governança Git/PR: apenas o Chefe ou você (Arquiteto) podem executar commits e operações de Pull Request na worktree de entrega.
Quando o usuário pedir para executar um comando, use run_shell: é o terminal desta sessão. Não diga que o terminal não existe. Comandos perigosos só rodam depois da aprovação do usuário.`
	case RoleDeveloper:
		return `Você é um desenvolvedor do time HiveMind. Responda em português.
Implemente somente a sua tarefa, dentro da sua worktree isolada. Use as ferramentas para ler, editar e executar comandos de compilação/teste permitidos.
EDIÇÕES CIRÚRGICAS & RAPIDEZ:
- O Chefe JÁ inspecionou a spec/plano e registrou os contratos e pontos importantes em [chief_plan] e [chief_context_brief]. NÃO fique relendo spec.md/plan.md/tasks.md inteiros em loop; leia apenas os arquivos de código que você realmente precisa editar/integrar (em lotes de no máximo 3-4 arquivos) e comece a implementar logo.
- Para alterar qualquer arquivo existente (ex.: .gitignore, configs, código), use SEMPRE edit_file (substituição exata que preserva encoding e CRLF/LF sem reescrever o arquivo inteiro). Reserve write_file apenas para criar arquivos novos ou reescritas completas.
- NUNCA use scripts Python/Node/PowerShell via run_shell apenas para editar texto de arquivos — use edit_file.
- Seja objetivo e econômico: em tarefas simples/pontuais, faça em 2-3 passos (read_file → edit_file → verificação rápida) e conclua sem voltas desnecessárias.
NÃO execute git commit, git push nem abra Pull Requests: no HiveMind somente o Chefe ou o Arquiteto realizam commits e operações de PR após aprovação do QA. Não altere arquivos de outros desenvolvedores. Se o QA devolver um pedido de ajuste, corrija só o que foi apontado.`
	case RoleQA:
		return `Você é o QA do time HiveMind. Responda em português.
Revise o diff e execute os testes com a ferramenta de shell. Se estiver tudo certo, chame qa_approve. Se encontrar falha, chame qa_request_changes com o que o desenvolvedor precisa corrigir e não aprove.
Não declare sucesso sem ter lido o resultado dos testes ou, na ausência de testes, sem ter inspecionado os arquivos alterados.`
	default:
		return "Responda em português e use as ferramentas disponíveis para cumprir a tarefa."
	}
}

// DefaultTeam builds the lean default squad: 1 Chief (Architect), base Developer(s), and 1 QA.
// When a task is large, the Chief/Architect can dynamically spawn 2..MaxDynamicDevs parallel
// developer instances from the base developer template without needing fixed dev entries.
func DefaultTeam(name, description, model, provider string, devs int) Team {
	if name == "" {
		name = "padrao"
	}
	if model == "" {
		model = defaultModel
	}
	if provider == "" {
		provider = "gemini"
	}
	if devs < 1 {
		devs = 1
	}
	agents := []Agent{
		{Name: "chefe", Role: RoleArchitect, Provider: provider, Model: model},
	}
	if devs == 1 {
		agents = append(agents, Agent{Name: "dev", Role: RoleDeveloper, Provider: provider, Model: model})
	} else {
		for i := 1; i <= devs; i++ {
			agents = append(agents, Agent{
				Name:     fmt.Sprintf("dev_%d", i),
				Role:     RoleDeveloper,
				Provider: provider,
				Model:    model,
			})
		}
	}
	agents = append(agents, Agent{Name: "qa", Role: RoleQA, Provider: provider, Model: model})
	t := Team{Name: name, Description: description, Agents: agents}
	for i := range t.Agents {
		t.Agents[i].Instructions = DefaultInstructions(t.Agents[i].Role)
		t.Agents[i].Provider = NormalizeProvider(t.Agents[i].Provider, t.Agents[i].Model)
	}
	return t
}

// FullSquadTeam builds a 5-role squad (pm, tech_lead, architect, dev(s), qa) for
// teams that explicitly opt into separate leadership specialists.
func FullSquadTeam(name, description, model, provider string, devs int) Team {
	if name == "" {
		name = "padrao"
	}
	if model == "" {
		model = defaultModel
	}
	if provider == "" {
		provider = "gemini"
	}
	if devs < 1 {
		devs = 1
	}
	agents := []Agent{
		{Name: "pm", Role: RoleProjectManager, Provider: provider, Model: model},
		{Name: "tech_lead", Role: RoleTechLead, Provider: provider, Model: model},
		{Name: "architect", Role: RoleArchitect, Provider: provider, Model: model},
	}
	if devs == 1 {
		agents = append(agents, Agent{Name: "dev_1", Role: RoleDeveloper, Provider: provider, Model: model})
	} else {
		for i := 1; i <= devs; i++ {
			agents = append(agents, Agent{
				Name:     fmt.Sprintf("dev_%d", i),
				Role:     RoleDeveloper,
				Provider: provider,
				Model:    model,
			})
		}
	}
	agents = append(agents, Agent{Name: "qa", Role: RoleQA, Provider: provider, Model: model})
	t := Team{Name: name, Description: description, Agents: agents}
	for i := range t.Agents {
		t.Agents[i].Instructions = DefaultInstructions(t.Agents[i].Role)
		t.Agents[i].Provider = NormalizeProvider(t.Agents[i].Provider, t.Agents[i].Model)
	}
	return t
}

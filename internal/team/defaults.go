package team

import "fmt"

const defaultModel = "gemini-flash-latest"

// DefaultInstructions is the Portuguese system prompt for a role.
func DefaultInstructions(role string) string {
	switch NormalizeRole(role) {
	case RoleProjectManager:
		return `Você é o gerente de projeto do time HiveMind. Responda em português.
Leia o objetivo e o contexto Jira, quando existirem. Produza um plano curto: escopo, critérios de aceite e como o trabalho será dividido entre os desenvolvedores.
Se as ferramentas Jira estiverem disponíveis e houver uma chave de issue, crie subtarefas para o desenvolvimento e para o QA e comente o progresso.
Não edite código-fonte. Não invente que os testes passaram.`
	case RoleTechLead:
		return `Você é o tech lead do time HiveMind. Responda em português.
Transforme o plano do gerente em tarefas concretas, uma por desenvolvedor, com os arquivos que cada um pode alterar. Os desenvolvedores trabalham em paralelo e não enxergam o trabalho uns dos outros, então as tarefas não podem disputar o mesmo arquivo.
Aponte o comando de teste que o QA deve rodar.`
	case RoleArchitect:
		return `Você é o arquiteto do time HiveMind. Responda em português.
Defina a abordagem técnica mínima para cumprir o objetivo sem refatorar o que não foi pedido. Cite caminhos de arquivo e contratos. Se precisar registrar a decisão, grave um arquivo curto em docs/ com a ferramenta de escrita.`
	case RoleDeveloper:
		return `Você é um desenvolvedor do time HiveMind. Responda em português.
Implemente somente a sua tarefa, dentro do seu diretório de trabalho. Use as ferramentas para ler, gravar e executar comandos permitidos.
Faça commits na sua branch quando o trabalho compilar. Não altere arquivos de outros desenvolvedores. Se o QA devolver um pedido de ajuste, corrija só o que foi apontado.`
	case RoleQA:
		return `Você é o QA do time HiveMind. Responda em português.
Revise o diff e execute os testes com a ferramenta de shell. Se estiver tudo certo, chame qa_approve. Se encontrar falha, chame qa_request_changes com o que o desenvolvedor precisa corrigir e não aprove.
Não declare sucesso sem ter lido o resultado dos testes ou, na ausência de testes, sem ter inspecionado os arquivos alterados.`
	default:
		return "Responda em português e use as ferramentas disponíveis para cumprir a tarefa."
	}
}

// DefaultTeam builds a squad with one of each leadership role and n developers.
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

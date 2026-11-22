package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// semanticSummaryInstruction is the fixed prompt that turns the rendered old
// conversation into the state carrier injected back into the request
// projection. It asks for consolidated state, not narrative: the goal is
// that the agent continues working without redoing finished steps.
const semanticSummaryInstruction = `Você compacta o histórico de uma conversa entre um agente de desenvolvimento e suas ferramentas.
Produza um resumo EM PORTUGUÊS, objetivo e estruturado, que permita continuar o trabalho sem reler o transcript:
1. Objetivo da tarefa em 1-2 frases.
2. Decisões e planos já estabelecidos (bullet points curtos).
3. Trabalho já concluído (arquivos criados/editados, testes rodados, comandos executados) — bullets curtos.
4. Pendências e próximos passos conhecidos.
5. Restrições ativas (gates de QA, orçamento, caminhos permitidos) e o último estado observado.
Máximo ~150 linhas. Não invente fatos; se algo for ambíguo, registre como pendência. Responda apenas com o resumo.

TRANSCRIPT A COMPACTAR:
`

// newConversationSummarizer returns the Summarizer wired into the contextx
// Budget of one agent. It performs one bounded GenerateContent call against
// the agent's own routed model (jev failover included) and fails open: any
// error simply makes contextx fall back to the heuristic structural cut, so
// a broken summarizer never blocks the run. The call's own usage flows
// through the model wrapper into the normal ledger.
func (r *Runner) newConversationSummarizer(a team.Agent) func(context.Context, string) (string, error) {
	return func(ctx context.Context, transcript string) (string, error) {
		if strings.TrimSpace(transcript) == "" {
			return "", fmt.Errorf("transcript vazio")
		}
		llm, err := r.modelFor(ctx, a)
		if err != nil {
			return "", err
		}
		req := &model.LLMRequest{
			Model: a.Model,
			Contents: []*genai.Content{{
				Role: "user",
				Parts: []*genai.Part{{
					Text: semanticSummaryInstruction + transcript,
				}},
			}},
		}
		var b strings.Builder
		for resp, callErr := range llm.GenerateContent(ctx, req, false) {
			if callErr != nil {
				return "", callErr
			}
			if resp == nil || resp.Content == nil {
				continue
			}
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text != "" {
					b.WriteString(p.Text)
				}
			}
		}
		out := strings.TrimSpace(b.String())
		if out == "" {
			return "", fmt.Errorf("resumo vazio do modelo")
		}
		return out, nil
	}
}

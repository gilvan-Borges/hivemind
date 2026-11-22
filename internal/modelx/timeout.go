package modelx

import (
	"context"
	"iter"
	"time"

	"google.golang.org/adk/v2/model"
)

// NewPerCallTimeout envolve qualquer model.LLM com um teto de tempo por
// chamada GenerateContent. Sem isso, um stream travado do provedor segura a
// rodada inteira do squad até cancelamento manual: o deadline cancela o ctx
// e o erro chega como context.DeadlineExceeded (classificado como outage
// pelo jev para fins de failover). O budget é por chamada — uma rodada com
// N chamadas tem N budgets frescos, não um só para o turno inteiro.
func NewPerCallTimeout(inner model.LLM, timeout time.Duration) model.LLM {
	if inner == nil || timeout <= 0 {
		return inner
	}
	return &perCallTimeoutLLM{inner: inner, timeout: timeout}
}

type perCallTimeoutLLM struct {
	inner   model.LLM
	timeout time.Duration
}

func (p *perCallTimeoutLLM) Name() string {
	if p.inner == nil {
		return ""
	}
	return p.inner.Name()
}

func (p *perCallTimeoutLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		callCtx, cancel := context.WithTimeout(ctx, p.timeout)
		defer cancel()
		for resp, err := range p.inner.GenerateContent(callCtx, req, stream) {
			if err != nil {
				yield(resp, err)
				return
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}

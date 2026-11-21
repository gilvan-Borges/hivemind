package githubx

import (
	"strings"
	"testing"
)

func sampleDelivery() []CrossLinkPR {
	return []CrossLinkPR{
		{Number: 12, Repo: "hivemind", URL: "https://github.com/acme/hivemind/pull/12", Title: "feat: Onda 3 — runtime único", Body: "Entrega da Onda 3.\n\n- Fatiamento tools\n- GHES"},
		{Number: 4, Repo: "HiveMind-App", URL: "https://github.com/acme/HiveMind-App/pull/4", Title: "feat: Onda 3 — app", Body: "Ajustes do app."},
	}
}

func TestCrossLinkOpenPRsLinksBothSides(t *testing.T) {
	prs := sampleDelivery()
	bodies := CrossLinkOpenPRs(prs)
	if len(bodies) != 2 {
		t.Fatalf("esperava 2 bodies, got %d", len(bodies))
	}
	root := bodies[12]
	if !strings.Contains(root, crossLinkHeader) {
		t.Fatalf("body do PR raiz sem seção de cross-link:\n%s", root)
	}
	if !strings.Contains(root, "https://github.com/acme/HiveMind-App/pull/4") {
		t.Fatalf("PR raiz deveria citar o PR irmão do app:\n%s", root)
	}
	if !strings.Contains(root, "(HiveMind-App)") {
		t.Fatalf("bullet do irmão deveria rotular o repo:\n%s", root)
	}
	if !strings.Contains(root, "Entrega da Onda 3.") {
		t.Fatalf("conteúdo original do body foi perdido:\n%s", root)
	}

	app := bodies[4]
	if !strings.Contains(app, "https://github.com/acme/hivemind/pull/12") {
		t.Fatalf("PR do app deveria citar o PR raiz:\n%s", app)
	}
	if !strings.Contains(app, "Ajustes do app.") {
		t.Fatalf("conteúdo original do app foi perdido:\n%s", app)
	}
}

func TestCrossLinkOpenPRsSinglePRIsNoop(t *testing.T) {
	prs := []CrossLinkPR{{Number: 1, Repo: "r", URL: "u", Title: "t", Body: "b"}}
	if bodies := CrossLinkOpenPRs(prs); bodies != nil {
		t.Fatalf("entrega com 1 PR não gera cross-link, got %v", bodies)
	}
	if bodies := CrossLinkOpenPRs(nil); bodies != nil {
		t.Fatalf("entrega vazia não gera cross-link, got %v", bodies)
	}
}

func TestCrossLinkIsIdempotent(t *testing.T) {
	prs := sampleDelivery()
	first := CrossLinkOpenPRs(prs)

	// Feed the updated bodies back in: the section must be REPLACED, not duplicated.
	for i := range prs {
		prs[i].Body = first[prs[i].Number]
	}
	second := CrossLinkOpenPRs(prs)
	for _, n := range []int{12, 4} {
		if strings.Count(second[n], crossLinkHeader) != 1 {
			t.Fatalf("seção duplicada no PR %d:\n%s", n, second[n])
		}
		if second[n] != first[n] {
			t.Fatalf("segunda execução não é estável (PR %d):\n---primeira---\n%s\n---segunda---\n%s", n, first[n], second[n])
		}
	}
}

func TestCrossLinkPreservesFollowingSections(t *testing.T) {
	body := "Descrição.\n\n## Checklist\n- [x] testes\n\n## PRs relacionados\n- link velho\n\n## Notas\nrodar migração"
	updated := BuildCrossLinkBody(body, []CrossLinkPR{
		{Number: 2, Repo: "outro", URL: "https://github.com/acme/outro/pull/2", Title: "irmão"},
	})
	if !strings.Contains(updated, "## Checklist") {
		t.Fatalf("seção anterior perdida:\n%s", updated)
	}
	if !strings.Contains(updated, "## Notas") {
		t.Fatalf("seção seguinte perdida:\n%s", updated)
	}
	if strings.Contains(updated, "link velho") {
		t.Fatalf("bullet antigo deveria ser substituído:\n%s", updated)
	}
	idxCheck := strings.Index(updated, "## Checklist")
	idxCross := strings.Index(updated, crossLinkHeader)
	idxNotes := strings.Index(updated, "## Notas")
	if !(idxCheck < idxCross && idxCross < idxNotes) {
		t.Fatalf("ordem das seções alterada:\n%s", updated)
	}
}

func TestCrossLinkSiblingsSortedDeterministically(t *testing.T) {
	prs := []CrossLinkPR{
		{Number: 9, Repo: "zeta", URL: "https://x/zeta/pull/9", Title: "z9"},
		{Number: 3, Repo: "alpha", URL: "https://x/alpha/pull/3", Title: "a3"},
		{Number: 1, Repo: "alpha", URL: "https://x/alpha/pull/1", Title: "a1", Body: ""},
	}
	sibs := siblingPRs(prs, prs[2])
	if len(sibs) != 2 {
		t.Fatalf("esperava 2 irmãos, got %d", len(sibs))
	}
	if sibs[0].Repo != "alpha" || sibs[0].Number != 3 || sibs[1].Repo != "zeta" {
		t.Fatalf("irmãos fora de ordem: %+v", sibs)
	}
}

func TestCrossLinkEmptyBodyStartsWithSection(t *testing.T) {
	updated := BuildCrossLinkBody("", []CrossLinkPR{
		{Number: 5, Repo: "app", URL: "https://x/app/pull/5", Title: "mobile"},
	})
	if !strings.HasPrefix(updated, crossLinkHeader) {
		t.Fatalf("body vazio deveria virar só a seção, got:\n%s", updated)
	}
}

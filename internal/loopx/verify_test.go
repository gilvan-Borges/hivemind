package loopx

import (
	"context"
	"strings"
	"testing"
)

type memTree struct {
	files   map[string]string
	commits int
	pr      string
	branch  string
}

func (m *memTree) Snapshot(context.Context, string) (string, map[string]string, error) {
	cp := map[string]string{}
	for k, v := range m.files {
		cp[k] = v
	}
	return fingerprint(cp), cp, nil
}

func (m *memTree) EnsureBranch(_ context.Context, _, branch string) error {
	m.branch = branch
	return nil
}

func (m *memTree) Commit(context.Context, string, string) (string, error) {
	m.commits++
	return "abc123", nil
}

func (m *memTree) OpenPR(context.Context, string, string, string, string) (string, error) {
	m.pr = "https://example.com/acme/pull/1"
	return m.pr, nil
}

type scriptJudge struct {
	verdict string
	calls   int
}

func (s *scriptJudge) Run(context.Context, JudgeRequest) (JudgeResult, error) {
	s.calls++
	return JudgeResult{Verdict: s.verdict, Reason: "evidência"}, nil
}

func TestProtectedPathBlocksDone(t *testing.T) {
	ws := t.TempDir()
	tree := &memTree{files: map[string]string{"internal/auth/user.go": "1"}}
	exec := &scriptExec{summary: "apaguei o teste"}
	execTouch := execFunc(func(req ExecRequest) {
		tree.files["internal/auth/user_test.go"] = "apagado"
		_ = req
	})
	c := &Controller{Executor: execTouch, Commands: scriptCmd{}, Tree: tree, Judge: &scriptJudge{verdict: JudgeMet}}
	off := false
	spec := Spec{
		ID: "fix-auth", Kind: KindGoal, Objective: "verde",
		Checks:         []Check{{Name: "unit", Run: "go test"}},
		ProtectedPaths: []string{"**/*_test.go"},
		Judge:          JudgeSpec{Enabled: &off},
		Limits:         LimitSpec{MaxIterations: 4, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "fix-auth", RunOptions{MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome == OutcomeDone || !strings.Contains(st.LastReason, "user_test.go") {
		t.Fatalf("deveria reprovar o teste protegido: %+v", st)
	}
	if tree.commits != 0 {
		t.Fatalf("não commita diff protegido: %d", tree.commits)
	}
	_ = exec
}

type execFunc func(ExecRequest)

func (f execFunc) Run(_ context.Context, req ExecRequest) (ExecResult, error) {
	f(req)
	return ExecResult{Summary: "mexi no teste"}, nil
}

func TestJudgeAndPullRequest(t *testing.T) {
	ws := t.TempDir()
	tree := &memTree{files: map[string]string{}}
	judge := &scriptJudge{verdict: JudgeMet}
	c := &Controller{Executor: &scriptExec{summary: "feito"}, Commands: scriptCmd{}, Judge: judge, Tree: tree}
	spec := Spec{
		ID: "fix-auth", Kind: KindGoal, Objective: "verde",
		Checks: []Check{{Name: "unit", Run: "go test"}},
		Limits: LimitSpec{MaxIterations: 3, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "fix-auth", RunOptions{Accompanied: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeDone || judge.calls != 1 || tree.commits != 1 || st.PRURL == "" {
		t.Fatalf("estado=%+v commits=%d judge=%d", st, tree.commits, judge.calls)
	}
	if tree.branch != "loop/fix-auth" {
		t.Fatalf("branch: %s", tree.branch)
	}
}

func TestImpossibleAfterTwoJudgeCalls(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{
		Executor: &scriptExec{summary: "não dá"},
		Commands: scriptCmd{},
		Judge:    &scriptJudge{verdict: JudgeImpossible},
	}
	spec := Spec{
		ID: "nunca", Kind: KindGoal, Objective: "impossível",
		Checks: []Check{{Name: "unit", Run: "go test"}},
		Limits: LimitSpec{MaxIterations: 6, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "nunca", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeImpossible || st.Iteration != 2 {
		t.Fatalf("estado: %+v", st)
	}
}

func TestFeatureHarnessOneItemPerIteration(t *testing.T) {
	ws := t.TempDir()
	var calls int
	exec := execFunc(func(req ExecRequest) {
		calls++
		if calls == 1 && !strings.Contains(req.Prompt, "JSON array") {
			t.Errorf("initializer sem pedido de features: %s", req.Prompt)
		}
	})
	c := &Controller{Executor: answeringExec{inner: exec}, Commands: scriptCmd{}, Judge: &scriptJudge{verdict: JudgeMet}}
	spec := Spec{
		ID: "harness", Kind: KindGoal, Objective: "duas partes",
		Features: "features.json",
		Checks:   []Check{{Name: "unit", Run: "go test"}},
		Limits:   LimitSpec{MaxIterations: 6, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "harness", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeDone || st.Iteration != 2 {
		t.Fatalf("estado: %+v", st)
	}
	items, _, err := LoadFeatures(ws, "harness", "features.json")
	if err != nil || !AllPass(items) {
		t.Fatalf("features: %+v %v", items, err)
	}
}

type answeringExec struct{ inner Executor }

func (a answeringExec) Run(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if a.inner != nil {
		_, _ = a.inner.Run(ctx, req)
	}
	if strings.Contains(req.Prompt, "JSON array") {
		return ExecResult{Summary: `[{"id":"a","title":"A","priority":1,"passes":true,"steps":["s"]}]`}, nil
	}
	return ExecResult{Summary: "item feito"}, nil
}

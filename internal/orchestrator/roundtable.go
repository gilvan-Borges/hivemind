package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

const (
	// defaultAutoCoachExploreFirst triggers the 1st live Chief intervention when a Dev
	// performs 10 exploratory reads/searches without editing any file.
	defaultAutoCoachExploreFirst = 10
	// defaultAutoCoachExploreSecond triggers a 2nd live Chief intervention if the Dev
	// reaches 20 exploratory reads/searches without editing any file.
	defaultAutoCoachExploreSecond = 20
	// defaultAutoCoachErrorStreak triggers live Chief intervention when a Dev hits
	// 2 consecutive tool/build/edit failures.
	defaultAutoCoachErrorStreak = 2
	// maxLiveChiefCallsPerRun bounds automatic + manual live Chief consultations per run
	// so the roundtable stays surgical and never inflates token costs.
	maxLiveChiefCallsPerRun = 6
)

// Steer injects a live roundtable directive from the user or supervisor into an
// active run without cancelling or restarting the Developer/QA execution.
func (r *Runner) Steer(author, message string) bool {
	if r == nil {
		return false
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		return false
	}
	who := strings.TrimSpace(author)
	if who == "" {
		who = "Usuário (Tech Lead)"
	}
	if r.sharedBoard != nil {
		r.sharedBoard.PublishDirective(who, "todos", msg)
	}
	if r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:   "handoff",
			Stage:  "dev",
			Author: "chefe",
			Label:  fmt.Sprintf("Mesa Redonda · Direcionamento ao Vivo (%s)", who),
			Text:   msg,
		})
	}
	return true
}

// runLiveChiefCoaching runs a single-shot live consultation with the Chief (Architect/PM)
// to guide a Developer or QA agent in real time during execution.
func (r *Runner) runLiveChiefCoaching(
	parentCtx context.Context,
	targetAgent team.Agent,
	targetADKName string,
	worktreeRoot string,
	triggerReason string,
	details string,
) string {
	if r == nil {
		return ""
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	r.roundtableMu.Lock()
	if r.roundtableCalls >= maxLiveChiefCallsPerRun {
		r.roundtableMu.Unlock()
		return r.synthesizeFallbackChiefDirective(targetADKName, worktreeRoot, triggerReason, details)
	}
	r.roundtableCalls++
	r.roundtableMu.Unlock()

	chief := r.primaryChief()
	directive := r.queryChiefRoundtableLLM(parentCtx, chief, targetAgent, targetADKName, worktreeRoot, triggerReason, details)
	if strings.TrimSpace(directive) == "" {
		directive = r.synthesizeFallbackChiefDirective(targetADKName, worktreeRoot, triggerReason, details)
	}
	directive = strings.TrimSpace(directive)
	if directive == "" {
		return ""
	}

	if r.sharedBoard != nil {
		r.sharedBoard.PublishDirective(chief.Name, targetADKName, directive)
	}
	if r.OnEvent != nil {
		stage := inferStage(targetADKName, roleLabel(targetAgent.Role))
		if stage == "" {
			stage = "dev"
		}
		r.OnEvent(Event{
			Kind:   "handoff",
			Stage:  stage,
			Author: chief.Name,
			Label:  fmt.Sprintf("Mesa Redonda · Chefe (%s) orientando %s ao vivo", chief.Name, targetADKName),
			Text:   directive,
		})
	}
	return directive
}

func (r *Runner) queryChiefRoundtableLLM(
	parentCtx context.Context,
	chief team.Agent,
	targetAgent team.Agent,
	targetADKName string,
	worktreeRoot string,
	triggerReason string,
	details string,
) string {
	// If unit tests wired a deterministic r.Models map (e.g. modelx.Scripted with exact
	// turn counts for Triage + Closing Chief) without an explicit LiveRoundtableModel,
	// use the deterministic fallback so scripted turn counters are not shifted.
	var llm model.LLM
	if r.LiveRoundtableModel != nil {
		llm = r.LiveRoundtableModel
	} else if r.Models != nil {
		return ""
	} else {
		ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)
		defer cancel()
		var err error
		llm, err = r.modelFor(ctx, chief)
		if err != nil || llm == nil {
			return ""
		}
	}

	ctx, cancel := context.WithTimeout(parentCtx, 25*time.Second)
	defer cancel()

	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Você é o Chefe Sênior (%s · %s) em uma MESA REDONDA AO VIVO com %s (%s).\n",
		chief.Name, roleLabel(chief.Role), targetADKName, roleLabel(targetAgent.Role))
	prompt.WriteString("Sua missão é dar um direcionamento técnico CIRÚRGICO, DIRETO e IMEDIATAMENTE ACIONÁVEL (máx. 6 a 10 linhas) para destravar ou corrigir a execução em tempo real:\n")
	prompt.WriteString("1. Indique EXATAMENTE qual(is) arquivo(s) criar ou editar agora (caminho relativo e classe/função/assinatura);\n")
	prompt.WriteString("2. Se o agente estiver explorando arquivos demais, diga para PARAR de buscar e aplicar a edição imediatamente nos arquivos certos;\n")
	prompt.WriteString("3. Se houver dependência de outra task ainda ausente na branch ou erro de build/teste, indique como desacoplar e fazer compilar/passar os testes da fatia atual;\n")
	prompt.WriteString("4. NÃO chame nenhuma ferramenta — responda somente com o texto direto de orientação técnica para o agente.\n\n")

	if obj := strings.TrimSpace(r.objectiveText); obj != "" {
		fmt.Fprintf(&prompt, "[Objetivo da Tarefa]\n%s\n\n", strx.TruncateBytes(obj, 1600, "\n…"))
	}
	if plan := strings.TrimSpace(r.chiefPlan); plan != "" {
		fmt.Fprintf(&prompt, "[Plano Técnico do Chefe]\n%s\n\n", strx.TruncateBytes(plan, 1800, "\n…"))
	}
	if brief := strings.TrimSpace(r.chiefContextBrief); brief != "" {
		fmt.Fprintf(&prompt, "[Dossiê Técnico (chief_context_brief)]\n%s\n\n", strx.TruncateBytes(brief, 1800, "\n…"))
	}
	fmt.Fprintf(&prompt, "[Motivo da Intervenção na Mesa Redonda]\n%s\n", strings.TrimSpace(triggerReason))
	if det := strings.TrimSpace(details); det != "" {
		fmt.Fprintf(&prompt, "Detalhes: %s\n", strx.TruncateBytes(det, 1200, "…"))
	}
	prompt.WriteByte('\n')

	if r.sharedBoard != nil {
		if trail := r.sharedBoard.RecentActivity(targetADKName); len(trail) > 0 {
			fmt.Fprintf(&prompt, "[Últimas ações executadas por %s]\n- %s\n\n", targetADKName, strings.Join(trail, "\n- "))
		}
		if summary := r.sharedBoard.SummaryFor(targetADKName); summary != "" {
			fmt.Fprintf(&prompt, "%s\n\n", strx.TruncateBytes(summary, 1200, "\n…"))
		}
	}
	if r.Git != nil && strings.TrimSpace(worktreeRoot) != "" && r.Git.IsRepo(ctx, worktreeRoot) {
		if st, err := r.Git.Status(ctx, worktreeRoot); err == nil {
			stTrim := strings.TrimSpace(st)
			if stTrim == "" {
				stTrim = "(nenhum arquivo criado ou modificado ainda na worktree)"
			}
			fmt.Fprintf(&prompt, "[Estado Atual da Worktree (%s)]\n%s\n", worktreeRoot, strx.TruncateBytes(stTrim, 1000, "\n…"))
		}
	}

	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(
				"Você é o Arquiteto/Tech Lead Chefe do HiveMind orientando sua equipe ao vivo na Mesa Redonda. Responda apenas com instruções técnicas diretas em português, sem chamar ferramentas.",
				genai.RoleUser,
			),
		},
		Contents: []*genai.Content{
			genai.NewContentFromText(prompt.String(), genai.RoleUser),
		},
	}

	var out strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil || resp == nil || resp.Content == nil {
			continue
		}
		if resp.Partial {
			continue
		}
		for _, p := range resp.Content.Parts {
			if p != nil && !p.Thought && strings.TrimSpace(p.Text) != "" {
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
				out.WriteString(strings.TrimSpace(p.Text))
			}
		}
	}
	return sanitizeHandoffText(out.String())
}

func (r *Runner) synthesizeFallbackChiefDirective(targetADKName, worktreeRoot, triggerReason, details string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Direcionamento direto do Chefe para **%s** (%s):\n", targetADKName, strings.TrimSpace(triggerReason))
	if det := strings.TrimSpace(details); det != "" {
		fmt.Fprintf(&b, "- Contexto reportado: %s\n", strx.TruncateBytes(det, 400, "…"))
	}
	if r.sharedBoard != nil {
		if trail := r.sharedBoard.RecentActivity(targetADKName); len(trail) > 0 {
			last := trail
			if len(last) > 5 {
				last = last[len(last)-5:]
			}
			fmt.Fprintf(&b, "- Você já inspecionou: `%s`. PARE de abrir novos arquivos exploratórios.\n", strings.Join(last, "`, `"))
		}
	}
	if plan := strings.TrimSpace(r.chiefPlan); plan != "" {
		fmt.Fprintf(&b, "- Foque estritamente no plano técnico: %s\n", strx.TruncateBytes(plan, 600, "…"))
	}
	if brief := strings.TrimSpace(r.chiefContextBrief); brief != "" {
		fmt.Fprintf(&b, "- Siga os contratos do dossiê: %s\n", strx.TruncateBytes(brief, 600, "…"))
	}
	b.WriteString("- Ação imediata: aplique agora as alterações com `write_file` (arquivos novos) ou `edit_file` (arquivos existentes) na worktree e valide os testes da sua fatia.")
	return b.String()
}

// newAskChiefTool gives Developers and QA a live tool to consult the Chief at any moment
// during execution without ending their turn.
func (r *Runner) newAskChiefTool(callerAgent team.Agent, callerADKName, worktreeRoot string) (tool.Tool, error) {
	type askChiefArgs struct {
		Question       string `json:"question" jsonschema:"Dúvida técnica concreta, bloqueio, dependência ausente ou erro de compilação/teste para o Chefe direcionar ao vivo na Mesa Redonda."`
		FilesInspected string `json:"files_inspected,omitempty" jsonschema:"Opcional: arquivos, classes ou linhas já inspecionados relacionados à dúvida."`
	}
	type askChiefRes struct {
		ChiefGuidance string `json:"chief_guidance"`
	}
	return functiontool.New(functiontool.Config{
		Name:        "ask_chief",
		Description: "Aciona a Mesa Redonda ao vivo com o Chefe (Arquiteto/Tech Lead) para receber direcionamento imediato sobre qual arquivo/classe editar, como tratar dependência ausente ou como corrigir erro de build/teste.",
	}, func(tctx agent.Context, args askChiefArgs) (askChiefRes, error) {
		q := strings.TrimSpace(args.Question)
		if q == "" {
			return askChiefRes{}, fmt.Errorf("informe question para consultar o Chefe na Mesa Redonda")
		}
		stage := inferStage(callerADKName, roleLabel(callerAgent.Role))
		if stage == "" {
			stage = "dev"
		}
		if r.OnEvent != nil {
			msg := q
			if fi := strings.TrimSpace(args.FilesInspected); fi != "" {
				msg += "\n- Arquivos inspecionados: `" + fi + "`"
			}
			r.OnEvent(Event{
				Kind:   "handoff",
				Stage:  stage,
				Author: callerADKName,
				Label:  fmt.Sprintf("Mesa Redonda · %s consultou o Chefe", callerADKName),
				Text:   msg,
			})
		}
		directive := r.runLiveChiefCoaching(
			tctx,
			callerAgent,
			callerADKName,
			worktreeRoot,
			fmt.Sprintf("Consulta ao vivo de %s na Mesa Redonda: %s", callerADKName, q),
			args.FilesInspected,
		)
		return askChiefRes{ChiefGuidance: directive}, nil
	})
}

// newLiveRoundtableSupervisorCallback monitors a Developer's live tool stream, records
// activity on TeamBoard, delivers any pending live user/Chief directives, and automatically
// brings the Chief into the Roundtable if the Developer stalls on exploration or hits
// consecutive errors.
func (r *Runner) newLiveRoundtableSupervisorCallback(
	devAgent team.Agent,
	devADKName string,
	devRoot string,
	roundFn func() int,
) llmagent.AfterToolCallback {
	initRound := 0
	if roundFn != nil {
		initRound = roundFn()
	}
	var (
		mu             sync.Mutex
		seenRound      = initRound
		exploreStreak  int
		writesInRound  int
		errorStreak    int
		coachedExplore map[int]bool
		coachedErrors  int
	)
	coachedExplore = make(map[int]bool)

	return func(tctx agent.Context, tl tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		summary := summarizeToolActionForBoard(name, args)
		if r.sharedBoard != nil && summary != "" {
			r.sharedBoard.RecordActivity(devADKName, summary)
		}

		toolFailed, errDetail := inspectToolFailure(name, result, err)

		mu.Lock()
		if roundFn != nil {
			if cur := roundFn(); cur != seenRound {
				seenRound = cur
				exploreStreak = 0
				writesInRound = 0
				errorStreak = 0
				coachedExplore = make(map[int]bool)
				coachedErrors = 0
			}
		}
		if devWriteTools[name] && !toolFailed {
			writesInRound++
			exploreStreak = 0
			errorStreak = 0
		} else if devExploreTools[name] && writesInRound == 0 {
			exploreStreak++
		}
		if toolFailed {
			errorStreak++
		} else if devWriteTools[name] || name == "run_shell" {
			errorStreak = 0
		}

		shouldCoachExplore := writesInRound == 0 &&
			(exploreStreak == defaultAutoCoachExploreFirst || exploreStreak == defaultAutoCoachExploreSecond) &&
			!coachedExplore[exploreStreak]
		if shouldCoachExplore {
			coachedExplore[exploreStreak] = true
		}
		curExplore := exploreStreak

		shouldCoachError := errorStreak >= defaultAutoCoachErrorStreak && coachedErrors < 2
		if shouldCoachError {
			coachedErrors++
			errorStreak = 0
		}
		mu.Unlock()

		var injectedBlocks []string

		// 1. Deliver any live directives queued by the user (SteerRun) or Chief on TeamBoard.
		if r.sharedBoard != nil {
			if pending := r.sharedBoard.PendingDirectivesFor(devADKName, true); len(pending) > 0 {
				var lines []string
				for _, d := range pending {
					lines = append(lines, fmt.Sprintf("- [%s → %s]: %s", d.From, d.To, d.Content))
				}
				injectedBlocks = append(injectedBlocks,
					"[🧭 MESA REDONDA — DIRECIONAMENTO AO VIVO RECEBIDO AGORA (SIGA IMEDIATAMENTE)]:\n"+strings.Join(lines, "\n"))
			}
		}

		// 2. Auto-intervene when the Developer explores 10 or 20 files without writing code.
		if shouldCoachExplore {
			directive := r.runLiveChiefCoaching(
				tctx,
				devAgent,
				devADKName,
				devRoot,
				fmt.Sprintf("Auto-supervisão na Mesa Redonda: %s executou %d leituras/buscas seguidas sem editar nenhum arquivo", devADKName, curExplore),
				summary,
			)
			if directive != "" {
				// Mark as delivered so we don't duplicate it from PendingDirectivesFor.
				if r.sharedBoard != nil {
					_ = r.sharedBoard.PendingDirectivesFor(devADKName, true)
				}
				injectedBlocks = append(injectedBlocks,
					"[🧭 MESA REDONDA — INTERVENÇÃO AO VIVO DO CHEFE (PARE DE EXPLORAR E EDITE OS ARQUIVOS INDICADOS AGORA)]:\n"+directive)
			}
		}

		// 3. Auto-intervene when the Developer hits consecutive tool/build/test errors.
		if shouldCoachError {
			directive := r.runLiveChiefCoaching(
				tctx,
				devAgent,
				devADKName,
				devRoot,
				fmt.Sprintf("Auto-supervisão na Mesa Redonda: %s encontrou falhas consecutivas ao executar %s", devADKName, name),
				errDetail,
			)
			if directive != "" {
				if r.sharedBoard != nil {
					_ = r.sharedBoard.PendingDirectivesFor(devADKName, true)
				}
				injectedBlocks = append(injectedBlocks,
					"[🧭 MESA REDONDA — CORREÇÃO AO VIVO DO CHEFE PARA DESTRAVAR O ERRO]:\n"+directive)
			}
		}

		if len(injectedBlocks) == 0 || result == nil {
			return nil, nil
		}
		joined := strings.Join(injectedBlocks, "\n\n")
		out := make(map[string]any, len(result)+1)
		for k, v := range result {
			out[k] = v
		}
		if txt, ok := result["text"].(string); ok {
			out["text"] = txt + "\n\n" + joined
		} else {
			out["roundtable_guidance"] = joined
		}
		return out, nil
	}
}

// runQARoundtableMediation brings the Chief into the Roundtable when QA calls qa_request_changes,
// translating the QA's rejection into a concrete action plan for the Developer's next iteration.
func (r *Runner) runQARoundtableMediation(
	tctx agent.Context,
	qaAgent team.Agent,
	qaRoot string,
	args, result map[string]any,
) {
	if r == nil || tctx == nil {
		return
	}
	var fb strings.Builder
	for _, k := range []string{"issues", "feedback", "reason", "summary", "text"} {
		if v, ok := args[k]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				if fb.Len() > 0 {
					fb.WriteString(" | ")
				}
				fb.WriteString(s)
			}
		}
	}
	if fb.Len() == 0 && result != nil {
		if v, ok := result["text"]; ok && v != nil {
			fb.WriteString(strings.TrimSpace(fmt.Sprint(v)))
		}
	}
	feedbackSummary := strings.TrimSpace(fb.String())
	if feedbackSummary == "" {
		if rawFB, err := tctx.ReadonlyState().Get("qa_feedback"); err == nil && rawFB != nil {
			feedbackSummary = strings.TrimSpace(fmt.Sprint(rawFB))
		}
	}

	directive := r.runLiveChiefCoaching(
		tctx,
		qaAgent,
		"dev",
		qaRoot,
		"Mediação da Mesa Redonda (Chefe + QA → Dev) após qa_request_changes",
		feedbackSummary,
	)
	if directive == "" {
		return
	}
	if actions := tctx.Actions(); actions != nil {
		if actions.StateDelta == nil {
			actions.StateDelta = map[string]any{}
		}
		existing := feedbackSummary
		if prev, ok := actions.StateDelta["qa_feedback"].(string); ok && strings.TrimSpace(prev) != "" {
			existing = strings.TrimSpace(prev)
		}
		actions.StateDelta["qa_feedback"] = existing + "\n\n[🧭 Direcionamento do Chefe na Mesa Redonda para corrigir esta reprovação do QA]:\n" + directive
	}
}

func summarizeToolActionForBoard(name string, args map[string]any) string {
	switch name {
	case "read_file":
		p := strings.TrimSpace(fmt.Sprint(args["path"]))
		s := parseIntArg(args["start_line"])
		e := parseIntArg(args["end_line"])
		if s > 0 || e > 0 {
			return fmt.Sprintf("read_file(%s:%d-%d)", p, s, e)
		}
		return fmt.Sprintf("read_file(%s)", p)
	case "edit_file", "write_file", "write_doc":
		return fmt.Sprintf("%s(%s)", name, strings.TrimSpace(fmt.Sprint(args["path"])))
	case "search_text":
		return fmt.Sprintf("search_text(%q em %s)", strings.TrimSpace(fmt.Sprint(args["query"])), strings.TrimSpace(fmt.Sprint(args["path"])))
	case "find_files":
		return fmt.Sprintf("find_files(%s)", strings.TrimSpace(fmt.Sprint(args["pattern"])))
	case "list_dir":
		return fmt.Sprintf("list_dir(%s)", strings.TrimSpace(fmt.Sprint(args["path"])))
	case "run_shell":
		cmd := strx.TruncateBytes(strings.TrimSpace(fmt.Sprint(args["command"])), 120, "…")
		return fmt.Sprintf("run_shell(%s)", cmd)
	default:
		return ""
	}
}

func inspectToolFailure(name string, result map[string]any, err error) (bool, string) {
	if err != nil {
		return true, err.Error()
	}
	if result == nil {
		return false, ""
	}
	if blocked, _ := result["blocked"].(bool); blocked {
		return true, strings.TrimSpace(fmt.Sprint(result["error"]))
	}
	if e, ok := result["error"]; ok && e != nil {
		if s := strings.TrimSpace(fmt.Sprint(e)); s != "" && s != "<nil>" {
			return true, strx.TruncateBytes(s, 500, "…")
		}
	}
	if code, ok := result["exit_code"]; ok && parseIntArg(code) != 0 {
		txt := strings.TrimSpace(fmt.Sprint(result["text"]))
		return true, strx.TruncateBytes(fmt.Sprintf("exit_code=%v: %s", code, txt), 500, "…")
	}
	if name == "run_shell" {
		if txt, ok := result["text"].(string); ok {
			low := strings.ToLower(txt)
			if strings.Contains(low, "build failure") || strings.Contains(low, "compilation failure") || strings.Contains(low, "exit status ") {
				return true, strx.TruncateBytes(strings.TrimSpace(txt), 500, "…")
			}
		}
	}
	return false, ""
}

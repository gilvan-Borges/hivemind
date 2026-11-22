package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

// MaxOnDemandSkillBodyBytes limita o corpo devolvido por read_skill. O limite é
// propositalmente maior que os 4 KiB do inline antigo: a skill entra no turno
// apenas quando o agente vai aplicá-la, então cabe dar contexto completo.
const MaxOnDemandSkillBodyBytes = 16 * 1024

type readSkillArgs struct {
	Name string `json:"name" jsonschema:"Nome exato da skill, conforme listado no bloco SKILLS SELECIONADAS do seu prompt."`
}

func (d Deps) readSkill(args readSkillArgs) (textResult, error) {
	if d.SkillLookup == nil {
		return textResult{}, fmt.Errorf("catálogo de skills indisponível para este agente")
	}
	name := strings.ToLower(strings.TrimSpace(args.Name))
	if name == "" {
		return textResult{}, fmt.Errorf("informe o nome exato da skill listada no bloco SKILLS SELECIONADAS")
	}
	skill, body, ok := d.SkillLookup(name)
	if !ok {
		return textResult{}, fmt.Errorf("skill %q não existe nos catálogos disponíveis para este agente", args.Name)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return textResult{}, fmt.Errorf("skill %q existe, mas o corpo do SKILL.md está vazio ou ilegível", name)
	}
	if skill.Path != "" {
		body += "\n\n(fonte: " + skill.Path + ")"
	}
	return textResult{Text: "[Diretrizes completas da skill `" + skill.Name + "`]\n" + body}, nil
}

func (d Deps) canReadSkill() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_skill",
		Description: "Carrega as diretrizes completas de uma skill selecionada (pelo nome exato listado no bloco SKILLS SELECIONADAS do seu prompt). Chame antes de executar a parte da tarefa coberta pela skill; não conta como leitura de exploração.",
	}, func(_ agent.Context, args readSkillArgs) (textResult, error) {
		return d.readSkill(args)
	})
}

// NewSkillLookup devolve o resolvedor nome→corpo usado por read_skill, lendo do
// catálogo do projeto (raiz do workspace) e do catálogo nativo da IDE. A lookup
// é barata (cache de diretório do skillx) e serve qualquer papel.
func NewSkillLookup(projectRoot, nativeRoot string, project, native []skillx.Skill) func(name string) (skillx.Skill, string, bool) {
	return NewSkillLookupWithPreferredNative(projectRoot, nativeRoot, project, native, nil)
}

// NewSkillLookupWithPreferredNative prioriza skills nativas explicitamente
// selecionadas/aprovadas pelo Chefe ou configuradas para o papel, evitando que
// uma cópia local de mesmo nome no projeto sombreie a versão nativa aprovada.
func NewSkillLookupWithPreferredNative(projectRoot, nativeRoot string, project, native, preferredNative []skillx.Skill) func(name string) (skillx.Skill, string, bool) {
	return func(name string) (skillx.Skill, string, bool) {
		if hit := skillx.Find(preferredNative, name); hit != nil {
			return *hit, hit.ReadBody(nativeRoot, MaxOnDemandSkillBodyBytes), true
		}
		if hit := skillx.Find(project, name); hit != nil {
			return *hit, hit.ReadBody(projectRoot, MaxOnDemandSkillBodyBytes), true
		}
		if hit := skillx.Find(native, name); hit != nil {
			return *hit, hit.ReadBody(nativeRoot, MaxOnDemandSkillBodyBytes), true
		}
		return skillx.Skill{}, "", false
	}
}

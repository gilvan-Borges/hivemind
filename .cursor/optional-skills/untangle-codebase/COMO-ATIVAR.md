# untangle-codebase (desligada)

Skill de limpeza agressiva do repositório inteiro, copiada de [caldoai/skills](https://github.com/caldoai/skills) (`skills/untangle-codebase/SKILL.md`, licença MIT em `LICENSE`). O Cursor não carrega `.cursor/optional-skills/`.

Só ative quando alguém pedir, com essas palavras, uma refatoração ou limpeza do repositório todo. Aí:

1. Copie esta pasta para `.cursor/skills/untangle-codebase/` (a pasta que o Cursor lê).
2. Não copie para `.agents/skills/`. A HiveMind IDE lê essa pasta e o Chefe pode delegar a skill ao time.
3. Revise o diff antes de considerar a limpeza pronta.
4. Ao terminar, apague `.cursor/skills/untangle-codebase/` e deixe só esta cópia em `optional-skills`.

Comando equivalente, se for reinstalar da fonte em vez de copiar esta pasta:

```bash
npx skills add caldoai/skills --skill untangle-codebase --agent cursor -y --copy
```

Esse comando grava em `.agents/skills/` na CLI atual. Mova o resultado para `.cursor/skills/untangle-codebase/` e apague a cópia em `.agents/skills/`.

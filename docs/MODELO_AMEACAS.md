# Modelo de ameaças

Ativos: prompt, resposta, diff, token de sessão, chave privada do box, cofre do sistema, política do residente e worktree.

O PC é a autoridade do repositório e da execução. Quem precisa de zero exposição de rede deixa o bridge desligado (`Enabled: false`, padrão).

Estado em 2026-11-20, depois do pareamento cifrado e da auditoria sem argv.

| Ameaça | Controle que está ligado |
| --- | --- |
| QR fotografado | Oferta de 128 bits, 5 minutos, um uso. A sessão nasce no resgate, dentro da caixa. O QR não leva `token=`. |
| Replay do resgate ou de um comando | A oferta é apagada antes do sucesso. `seq` menor ou igual não executa. Cada caixa sorteia nonce de 24 bytes. |
| Sniffer na LAN | Corpo autenticado e SSE são `naclbox`. `health` não leva segredo. O portador mostra IP, tamanho e tempo. |
| Token no disco do PC | Só SHA-256 em `remote-bridge.json`. A privada de longo prazo vai para o cofre com o nome `lan-bridge-box`. Arquivo antigo com `token` é hasheado e o campo cru é apagado. Bearer em claro deixa de valer. |
| Cofre indisponível no resgate | O resgate falha. A privada não é gravada em JSON. |
| Downgrade para o pareamento antigo | O servidor recusa Bearer claro e URI com `token=`. |
| Relay no caminho | O relay não ganhou rota nova e não recebe a privada. Ele continua vendo IP, tamanho e tempo. |
| Auditoria copiar o comando | `audit.jsonl` guarda `targetClass` e `targetHash`. Não guarda caminho nem argv. Falha ao gravar a linha ainda aborta o efeito. |
| Sandbox contornado | Quem executa é o broker, que chama o sandbox e falha fechado quando o mecanismo não está disponível. Esta fase não cria outro caminho de sucesso. |
| Dois núcleos do residente | Ainda possível: o mutex de instância única não existe ainda. O risco permanece até a casca fora da IDE. |

Manifesto de detector, monitor de processo e o processo do orb não estão ligados. Não há controle novo para eles neste arquivo.

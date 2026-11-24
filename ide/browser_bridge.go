package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/browserx"
)

func (a *App) startBrowserBridge() {
	browserx.SetChatHandler(a.browserChat)
	if _, err := browserx.Listen(a.ctx); err != nil {
		log.Printf("aviso: ponte do Chrome indisponível: %v", err)
	}
}

func (a *App) browserChat(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("mensagem vazia")
	}
	ws := strings.TrimSpace(a.initialDir)
	if ws == "" {
		if recent := idecore.LoadRecentProjects(""); len(recent) > 0 {
			ws = recent[0]
		}
	}
	if ws == "" {
		return "", fmt.Errorf("abra um projeto na IDE para o Agent Hive receber o chat")
	}
	if a.SteerRun("lane-hive", text) {
		return "instrução enviada para a execução em andamento", nil
	}
	go func() { _, _ = a.StartRun("lane-hive", ws, "", text, "", "") }()
	return "turno do Agent Hive iniciado na IDE", nil
}

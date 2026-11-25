package main

import (
	"fmt"
	"log"
	"strings"

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
	if a.resident == nil {
		return "", fmt.Errorf("Agent Hive indisponível")
	}
	res, err := a.resident.RunBrowserExtensionTurn(a.ctx, text)
	if err != nil {
		return "", err
	}
	reply := strings.TrimSpace(res.FinalText)
	if reply == "" {
		return "Concluído.", nil
	}
	return reply, nil
}

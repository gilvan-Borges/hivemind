package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gilvan-Borges/HiveMind/internal/relay"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("hivemind-relay: ")

	st := relay.SettingsFromEnv()
	fs := flag.NewFlagSet("hivemind-relay", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", "", "endereço de escuta (padrão $HIVEMIND_RELAY_ADDR ou 127.0.0.1:8787)")
	data := fs.String("data", "", "diretório do SQLite (padrão $HIVEMIND_RELAY_DATA ou ./data)")
	publicURL := fs.String("public-url", "", "URL https pública usada no QR de vínculo")
	showVersion := fs.Bool("version", false, "mostra a versão e sai")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: hivemind-relay [--addr host:porta] [--data dir] [--public-url https://relay.exemplo]\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nVariáveis: HIVEMIND_RELAY_ADDR, HIVEMIND_RELAY_DATA, HIVEMIND_RELAY_PUBLIC_URL,\n")
		fmt.Fprintf(os.Stderr, "HIVEMIND_SEED_EMAIL, HIVEMIND_SEED_PASSWORD, HIVEMIND_RELAY_TLS_CERT, HIVEMIND_RELAY_TLS_KEY.\n")
		fmt.Fprintf(os.Stderr, "Sem certificados, o processo fala HTTP e deve ficar atrás do Caddy (HTTPS obrigatório na internet).\n")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(relay.Version)
		return
	}
	if *addr != "" {
		st.Addr = *addr
	}
	if *data != "" {
		st.DataDir = *data
	}
	if *publicURL != "" {
		st.PublicURL = *publicURL
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := relay.Run(ctx, st); err != nil {
		log.Fatal(err)
	}
}

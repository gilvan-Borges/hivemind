package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gilvan-Borges/HiveMind/internal/relay"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("hivemind-relay: ")

	if len(os.Args) > 1 && os.Args[1] == "account" {
		if err := runAccountCmd(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}

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

func runAccountCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: hivemind-relay account <set|list> [flags]")
	}
	st := relay.SettingsFromEnv()
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("account set", flag.ContinueOnError)
		email := fs.String("email", "", "e-mail ou usuário da conta")
		password := fs.String("password", "", "senha (mínimo 8 caracteres)")
		role := fs.String("role", "owner", "papel da conta: owner ou member")
		data := fs.String("data", st.DataDir, "diretório do SQLite")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *email == "" || *password == "" {
			return fmt.Errorf("--email e --password são obrigatórios")
		}
		if err := os.MkdirAll(*data, 0o700); err != nil {
			return err
		}
		store, err := relay.Open(filepath.Join(*data, "relay.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		acct, created, err := store.UpsertAccount(*email, *password, *role, relay.DefaultArgon())
		if err != nil {
			return err
		}
		action := "atualizada"
		if created {
			action = "criada"
		}
		fmt.Printf("Conta %s (%s): %s (papel: %s)\n", action, acct.ID, acct.Email, acct.Role)
		return nil
	case "list":
		fs := flag.NewFlagSet("account list", flag.ContinueOnError)
		data := fs.String("data", st.DataDir, "diretório do SQLite")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := os.MkdirAll(*data, 0o700); err != nil {
			return err
		}
		store, err := relay.Open(filepath.Join(*data, "relay.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		accounts, err := store.ListAccounts()
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			fmt.Println("Nenhuma conta cadastrada.")
			return nil
		}
		for _, a := range accounts {
			fmt.Printf("%s\t%s\t%s\n", a.ID, a.Email, a.Role)
		}
		return nil
	default:
		return fmt.Errorf("subcomando desconhecido %q (use: set, list)", args[0])
	}
}

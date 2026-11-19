package relay

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version is reported by GET /api/v1/health.
const Version = "0.1.0"

// Settings is the process configuration, normally filled from the environment.
type Settings struct {
	Addr         string
	DataDir      string
	PublicURL    string
	SeedEmail    string
	SeedPassword string
	TLSCert      string
	TLSKey       string
	Ready        func(addr string)
}

// SettingsFromEnv reads HIVEMIND_* variables. Flags in main override empty values.
func SettingsFromEnv() Settings {
	return Settings{
		Addr:         envOr("HIVEMIND_RELAY_ADDR", "127.0.0.1:8787"),
		DataDir:      envOr("HIVEMIND_RELAY_DATA", "data"),
		PublicURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("HIVEMIND_RELAY_PUBLIC_URL")), "/"),
		SeedEmail:    strings.TrimSpace(os.Getenv("HIVEMIND_SEED_EMAIL")),
		SeedPassword: os.Getenv("HIVEMIND_SEED_PASSWORD"),
		TLSCert:      strings.TrimSpace(os.Getenv("HIVEMIND_RELAY_TLS_CERT")),
		TLSKey:       strings.TrimSpace(os.Getenv("HIVEMIND_RELAY_TLS_KEY")),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Run opens the database, seeds the owner once, and serves until ctx is done.
func Run(ctx context.Context, st Settings) error {
	if st.Addr == "" {
		st.Addr = "127.0.0.1:8787"
	}
	if st.DataDir == "" {
		st.DataDir = "data"
	}
	if err := os.MkdirAll(st.DataDir, 0o700); err != nil {
		return err
	}
	if (st.TLSCert == "") != (st.TLSKey == "") {
		return fmt.Errorf("defina HIVEMIND_RELAY_TLS_CERT e HIVEMIND_RELAY_TLS_KEY juntos, ou nenhum dos dois")
	}
	dbPath := filepath.Join(st.DataDir, "relay.db")
	store, err := Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := seedOwner(store, st); err != nil {
		return err
	}

	srv, err := New(store, Options{PublicURL: st.PublicURL})
	if err != nil {
		return err
	}
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := store.Cleanup(time.Now()); err != nil {
					log.Printf("relay: limpeza: %v", err)
				}
			}
		}
	}()

	ln, err := net.Listen("tcp", st.Addr)
	if err != nil {
		return err
	}
	if st.Ready != nil {
		st.Ready(ln.Addr().String())
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		var serveErr error
		if st.TLSCert != "" {
			log.Printf("relay: https em %s (tls próprio)", ln.Addr())
			serveErr = httpSrv.ServeTLS(ln, st.TLSCert, st.TLSKey)
		} else {
			if !isLoopbackAddr(ln.Addr().String()) {
				log.Printf("relay: aviso: escutando %s sem TLS. Coloque o Caddy na frente ou defina o par de certificados.", ln.Addr())
			}
			log.Printf("relay: http em %s", ln.Addr())
			serveErr = httpSrv.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			errc <- serveErr
		}
	}()
	select {
	case <-ctx.Done():
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shctx)
	case err := <-errc:
		return err
	}
}

func seedOwner(store *Store, st Settings) error {
	n, err := store.CountAccounts()
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("relay: seed ignorado, já existe conta")
		return nil
	}
	email := st.SeedEmail
	if email == "" {
		email = "owner@localhost"
	}
	password := st.SeedPassword
	generated := false
	if password == "" {
		password, err = generatePassword(20)
		if err != nil {
			return err
		}
		generated = true
	}
	if _, err := store.CreateAccount(email, password, "owner", DefaultArgon()); err != nil {
		return fmt.Errorf("semeando owner: %w", err)
	}
	if generated {
		path := filepath.Join(st.DataDir, "initial-password.txt")
		content := fmt.Sprintf("email: %s\nsenha: %s\n", email, password)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "HiveMind Relay: senha inicial do owner (anote e apague o arquivo)\n  email: %s\n  senha: %s\n  arquivo: %s\n", email, password, path)
		return nil
	}
	log.Printf("relay: conta owner semeada a partir do ambiente (%s)", email)
	return nil
}

func generatePassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out), nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

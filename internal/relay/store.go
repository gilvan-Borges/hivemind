package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/relaywire"
	_ "modernc.org/sqlite"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	linkTTL    = 10 * time.Minute
	rpcTTL     = 3 * time.Minute
	eventKeep  = 400
)

// Account is a relay user. Accounts never share devices or events.
type Account struct {
	ID    string
	Email string
	Role  string
}

// Device is one IDE linked to an account.
type Device struct {
	ID           string
	AccountID    string
	Name         string
	IdePublicKey []byte
	AppPublicKey []byte
	CreatedAt    time.Time
	LastSeenAt   time.Time
	RevokedAt    time.Time
	EventSeq     int64
}

// Revoked reports whether the device credential must be rejected.
func (d Device) Revoked() bool { return !d.RevokedAt.IsZero() }

// E2EReady is true when both sides have published an X25519 public key.
func (d Device) E2EReady() bool {
	return len(d.IdePublicKey) == 32 && len(d.AppPublicKey) == 32
}

// LinkCode is a short-lived pairing code bound to one account.
type LinkCode struct {
	Code         string
	AccountID    string
	Initiator    string
	Name         string
	IdePublicKey []byte
	AppPublicKey []byte
	DeviceID     string
	DeviceToken  string
	ExpiresAt    time.Time
	ConsumedAt   time.Time
}

// StoredEvent is one durable IDE→app frame.
type StoredEvent struct {
	Seq  int64
	Body []byte
}

// QueuedRPC is an app→IDE call waiting for the IDE to reconnect.
type QueuedRPC struct {
	ID   string
	Body []byte
}

// Store is the SQLite persistence for the relay. One connection keeps memory
// and lock behaviour predictable on a 1 GB VM.
type Store struct {
	db *sql.DB
}

// Open creates or opens the database at path (file mode 0600, directory 0700).
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	pragmas := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA cache_size = -2000`,
		`PRAGMA temp_store = MEMORY`,
		`PRAGMA mmap_size = 0`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  refresh_hash TEXT NOT NULL UNIQUE,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER,
  created_at INTEGER NOT NULL,
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS access_tokens (
  hash TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS refresh_denylist (
  hash TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  ide_public_key BLOB,
  app_public_key BLOB,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER,
  revoked_at INTEGER,
  event_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS devices_account ON devices(account_id);
CREATE TABLE IF NOT EXISTS link_codes (
  code TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  initiator TEXT NOT NULL,
  name TEXT NOT NULL,
  ide_public_key BLOB,
  app_public_key BLOB,
  device_id TEXT,
  device_token TEXT,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS event_log (
  device_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  body BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (device_id, seq)
);
CREATE TABLE IF NOT EXISTS rpc_queue (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL,
  account_id TEXT NOT NULL,
  body BLOB NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS qr_tickets (
  ticket_hash TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
`)
	return err
}

func newID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func newSecret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 3 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "e-mail ou usuário inválido"}
	}
	if strings.Contains(email, "@") && (strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@")) {
		return "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "e-mail ou usuário inválido"}
	}
	for _, r := range email {
		if r < 0x20 || r == 0x7f {
			return "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "e-mail ou usuário inválido"}
		}
	}
	return email, nil
}

// CountAccounts returns how many accounts exist (used by seeding).
func (s *Store) CountAccounts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n)
	return n, err
}

// CreateAccount inserts a user. role is "owner" or "member".
func (s *Store) CreateAccount(email, password, role string, params Argon) (Account, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Account{}, err
	}
	if role != "owner" && role != "member" {
		return Account{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "papel inválido"}
	}
	hash, err := HashPassword(password, params)
	if err != nil {
		return Account{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: err.Error()}
	}
	id, err := newID("acct_")
	if err != nil {
		return Account{}, err
	}
	_, err = s.db.Exec(`INSERT INTO accounts(id, email, password_hash, role, created_at) VALUES(?,?,?,?,?)`,
		id, email, hash, role, time.Now().UTC().UnixMilli())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Account{}, &httpError{Status: 409, Code: relaywire.CodeConflict, Msg: "já existe uma conta com esse e-mail ou usuário"}
		}
		return Account{}, err
	}
	return Account{ID: id, Email: email, Role: role}, nil
}

// UpsertAccount creates or updates an account (password and role) for CLI admin provisioning.
func (s *Store) UpsertAccount(email, password, role string, params Argon) (Account, bool, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Account{}, false, err
	}
	if role != "owner" && role != "member" {
		return Account{}, false, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "papel inválido"}
	}
	hash, err := HashPassword(password, params)
	if err != nil {
		return Account{}, false, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: err.Error()}
	}
	var existingID string
	err = s.db.QueryRow(`SELECT id FROM accounts WHERE email=?`, email).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		id, idErr := newID("acct_")
		if idErr != nil {
			return Account{}, false, idErr
		}
		if _, insErr := s.db.Exec(`INSERT INTO accounts(id, email, password_hash, role, created_at) VALUES(?,?,?,?,?)`,
			id, email, hash, role, time.Now().UTC().UnixMilli()); insErr != nil {
			return Account{}, false, insErr
		}
		return Account{ID: id, Email: email, Role: role}, true, nil
	}
	if err != nil {
		return Account{}, false, err
	}
	if _, err := s.db.Exec(`UPDATE accounts SET password_hash=?, role=? WHERE id=?`, hash, role, existingID); err != nil {
		return Account{}, false, err
	}
	if err := s.RevokeAllSessions(existingID); err != nil {
		return Account{}, false, err
	}
	return Account{ID: existingID, Email: email, Role: role}, false, nil
}

// ListAccounts returns all registered relay accounts ordered by creation time.
func (s *Store) ListAccounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT id, email, role FROM accounts ORDER BY created_at ASC, email ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Email, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Authenticate verifies email and password. Missing accounts still run argon2id
// against dummyHash so the response time does not reveal which emails exist.
func (s *Store) Authenticate(email, password, dummyHash string) (Account, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return Account{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "credenciais inválidas"}
	}
	var acct Account
	var hash string
	err = s.db.QueryRow(`SELECT id, email, role, password_hash FROM accounts WHERE email=?`, email).
		Scan(&acct.ID, &acct.Email, &acct.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = VerifyPassword(password, dummyHash)
		return Account{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "credenciais inválidas"}
	}
	if err != nil {
		return Account{}, err
	}
	ok, verr := VerifyPassword(password, hash)
	if verr != nil || !ok {
		return Account{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "credenciais inválidas"}
	}
	return acct, nil
}

type issuedSession struct {
	Account      Account
	SessionID    string
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

func (s *Store) issue(accountID, userAgent string, accessFor, refreshFor time.Duration) (issuedSession, error) {
	var acct Account
	err := s.db.QueryRow(`SELECT id, email, role FROM accounts WHERE id=?`, accountID).Scan(&acct.ID, &acct.Email, &acct.Role)
	if err != nil {
		return issuedSession{}, err
	}
	sid, err := newID("ses_")
	if err != nil {
		return issuedSession{}, err
	}
	access, err := newSecret(relaywire.TokenAccessPrefix)
	if err != nil {
		return issuedSession{}, err
	}
	refresh, err := newSecret(relaywire.TokenRefreshPrefix)
	if err != nil {
		return issuedSession{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return issuedSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO sessions(id, account_id, refresh_hash, expires_at, created_at, user_agent) VALUES(?,?,?,?,?,?)`,
		sid, accountID, tokenHash(refresh), now.Add(refreshFor).UnixMilli(), now.UnixMilli(), trimUA(userAgent)); err != nil {
		return issuedSession{}, err
	}
	if _, err := tx.Exec(`INSERT INTO access_tokens(hash, session_id, account_id, expires_at) VALUES(?,?,?,?)`,
		tokenHash(access), sid, accountID, now.Add(accessFor).UnixMilli()); err != nil {
		return issuedSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return issuedSession{}, err
	}
	return issuedSession{
		Account: acct, SessionID: sid, AccessToken: access, RefreshToken: refresh,
		ExpiresIn: int(accessFor.Seconds()),
	}, nil
}

// Login creates a session after a successful password check.
func (s *Store) Login(email, password, dummyHash, userAgent string, paramsIgnored Argon, accessFor, refreshFor time.Duration) (issuedSession, error) {
	acct, err := s.Authenticate(email, password, dummyHash)
	if err != nil {
		return issuedSession{}, err
	}
	return s.issue(acct.ID, userAgent, accessFor, refreshFor)
}

// Refresh rotates the refresh token and the access token. Reuse of an already
// rotated refresh token revokes the whole session.
func (s *Store) Refresh(refresh, userAgent string, accessFor, refreshFor time.Duration) (issuedSession, error) {
	if !strings.HasPrefix(refresh, relaywire.TokenRefreshPrefix) {
		return issuedSession{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "refresh token inválido"}
	}
	hash := tokenHash(refresh)
	var deniedSession string
	err := s.db.QueryRow(`SELECT session_id FROM refresh_denylist WHERE hash=?`, hash).Scan(&deniedSession)
	if err == nil {
		_ = s.RevokeSession(deniedSession)
		return issuedSession{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "refresh token reutilizado; sessão revogada"}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return issuedSession{}, err
	}
	var sessionID, accountID string
	var exp int64
	var revoked sql.NullInt64
	err = s.db.QueryRow(`SELECT id, account_id, expires_at, revoked_at FROM sessions WHERE refresh_hash=?`, hash).
		Scan(&sessionID, &accountID, &exp, &revoked)
	if errors.Is(err, sql.ErrNoRows) || revoked.Valid || time.Now().UTC().UnixMilli() > exp {
		return issuedSession{}, &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: "refresh token inválido"}
	}
	if err != nil {
		return issuedSession{}, err
	}
	access, err := newSecret(relaywire.TokenAccessPrefix)
	if err != nil {
		return issuedSession{}, err
	}
	next, err := newSecret(relaywire.TokenRefreshPrefix)
	if err != nil {
		return issuedSession{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return issuedSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO refresh_denylist(hash, session_id, expires_at) VALUES(?,?,?)`,
		hash, sessionID, exp); err != nil {
		return issuedSession{}, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET refresh_hash=?, expires_at=?, user_agent=? WHERE id=?`,
		tokenHash(next), now.Add(refreshFor).UnixMilli(), trimUA(userAgent), sessionID); err != nil {
		return issuedSession{}, err
	}
	if _, err := tx.Exec(`DELETE FROM access_tokens WHERE session_id=?`, sessionID); err != nil {
		return issuedSession{}, err
	}
	if _, err := tx.Exec(`INSERT INTO access_tokens(hash, session_id, account_id, expires_at) VALUES(?,?,?,?)`,
		tokenHash(access), sessionID, accountID, now.Add(accessFor).UnixMilli()); err != nil {
		return issuedSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return issuedSession{}, err
	}
	var acct Account
	if err := s.db.QueryRow(`SELECT id, email, role FROM accounts WHERE id=?`, accountID).Scan(&acct.ID, &acct.Email, &acct.Role); err != nil {
		return issuedSession{}, err
	}
	return issuedSession{Account: acct, SessionID: sessionID, AccessToken: access, RefreshToken: next, ExpiresIn: int(accessFor.Seconds())}, nil
}

// LookupAccess resolves a live access token to its account and session.
func (s *Store) LookupAccess(token string) (Account, string, error) {
	if !strings.HasPrefix(token, relaywire.TokenAccessPrefix) {
		return Account{}, "", errUnauthorized("token de acesso inválido")
	}
	var acct Account
	var sessionID string
	var exp int64
	var revoked sql.NullInt64
	err := s.db.QueryRow(`
		SELECT a.id, a.email, a.role, t.session_id, t.expires_at, s.revoked_at
		FROM access_tokens t
		JOIN accounts a ON a.id = t.account_id
		JOIN sessions s ON s.id = t.session_id
		WHERE t.hash=?`, tokenHash(token)).Scan(&acct.ID, &acct.Email, &acct.Role, &sessionID, &exp, &revoked)
	if errors.Is(err, sql.ErrNoRows) || revoked.Valid || time.Now().UTC().UnixMilli() > exp {
		return Account{}, "", errUnauthorized("token de acesso inválido")
	}
	if err != nil {
		return Account{}, "", err
	}
	return acct, sessionID, nil
}

// LookupDevice resolves a device credential. Revoked devices fail.
func (s *Store) LookupDevice(token string) (Device, error) {
	if !strings.HasPrefix(token, relaywire.TokenDevicePrefix) {
		return Device{}, errUnauthorized("credencial de IDE inválida")
	}
	d, err := s.deviceByHash(tokenHash(token))
	if err != nil {
		return Device{}, errUnauthorized("credencial de IDE inválida")
	}
	if d.Revoked() {
		return Device{}, errUnauthorized("IDE revogada")
	}
	return d, nil
}

func (s *Store) deviceByHash(hash string) (Device, error) {
	row := s.db.QueryRow(`SELECT id, account_id, name, ide_public_key, app_public_key, created_at, last_seen_at, revoked_at, event_seq FROM devices WHERE token_hash=?`, hash)
	return scanDevice(row)
}

// RevokeSession marks a session revoked and drops its access tokens.
func (s *Store) RevokeSession(sessionID string) error {
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE sessions SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, now, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM access_tokens WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeByRefresh revokes the session that owns this refresh token.
func (s *Store) RevokeByRefresh(refresh string) (string, error) {
	if !strings.HasPrefix(refresh, relaywire.TokenRefreshPrefix) {
		return "", errUnauthorized("refresh token inválido")
	}
	hash := tokenHash(refresh)
	var sessionID string
	err := s.db.QueryRow(`SELECT id FROM sessions WHERE refresh_hash=?`, hash).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRow(`SELECT session_id FROM refresh_denylist WHERE hash=?`, hash).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", errUnauthorized("refresh token inválido")
		}
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := s.RevokeSession(sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

// RevokeAllSessions revokes every session of an account.
func (s *Store) RevokeAllSessions(accountID string) error {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE account_id=? AND revoked_at IS NULL`, accountID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		if err := s.RevokeSession(id); err != nil {
			return err
		}
	}
	return nil
}

// ChangePassword verifies the current password, stores a new hash and revokes
// every existing session. The caller receives a fresh session.
func (s *Store) ChangePassword(accountID, current, next, userAgent string, params Argon, accessFor, refreshFor time.Duration) (issuedSession, error) {
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM accounts WHERE id=?`, accountID).Scan(&hash); err != nil {
		return issuedSession{}, err
	}
	ok, err := VerifyPassword(current, hash)
	if err != nil || !ok {
		return issuedSession{}, errUnauthorized("senha atual incorreta")
	}
	encoded, err := HashPassword(next, params)
	if err != nil {
		return issuedSession{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: err.Error()}
	}
	if _, err := s.db.Exec(`UPDATE accounts SET password_hash=? WHERE id=?`, encoded, accountID); err != nil {
		return issuedSession{}, err
	}
	if err := s.RevokeAllSessions(accountID); err != nil {
		return issuedSession{}, err
	}
	return s.issue(accountID, userAgent, accessFor, refreshFor)
}

// CreateDevice inserts an IDE and returns its one-time credential.
func (s *Store) CreateDevice(accountID, name string, idePub, appPub []byte) (Device, string, error) {
	name = cleanName(name)
	if name == "" {
		return Device{}, "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "nome da IDE obrigatório"}
	}
	if len(idePub) > 0 && len(idePub) != 32 {
		return Device{}, "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "chave pública da IDE inválida"}
	}
	if len(appPub) > 0 && len(appPub) != 32 {
		return Device{}, "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "chave pública do app inválida"}
	}
	id, err := newID("dev_")
	if err != nil {
		return Device{}, "", err
	}
	token, err := newSecret(relaywire.TokenDevicePrefix)
	if err != nil {
		return Device{}, "", err
	}
	now := time.Now().UTC().UnixMilli()
	_, err = s.db.Exec(`INSERT INTO devices(id, account_id, name, token_hash, ide_public_key, app_public_key, created_at) VALUES(?,?,?,?,?,?,?)`,
		id, accountID, name, tokenHash(token), nullBytes(idePub), nullBytes(appPub), now)
	if err != nil {
		return Device{}, "", err
	}
	d, err := s.DeviceForAccount(accountID, id)
	return d, token, err
}

// ListDevices returns non-revoked IDEs of one account, newest first.
func (s *Store) ListDevices(accountID string) ([]Device, error) {
	rows, err := s.db.Query(`SELECT id, account_id, name, ide_public_key, app_public_key, created_at, last_seen_at, revoked_at, event_seq FROM devices WHERE account_id=? AND revoked_at IS NULL ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceForAccount loads a device only when it belongs to the account.
func (s *Store) DeviceForAccount(accountID, deviceID string) (Device, error) {
	row := s.db.QueryRow(`SELECT id, account_id, name, ide_public_key, app_public_key, created_at, last_seen_at, revoked_at, event_seq FROM devices WHERE id=? AND account_id=?`, deviceID, accountID)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, errNotFound("IDE não encontrada")
	}
	return d, err
}

// RevokeDevice revokes an IDE of the account. Other accounts get not-found.
func (s *Store) RevokeDevice(accountID, deviceID string) error {
	res, err := s.db.Exec(`UPDATE devices SET revoked_at=? WHERE id=? AND account_id=? AND revoked_at IS NULL`,
		time.Now().UTC().UnixMilli(), deviceID, accountID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errNotFound("IDE não encontrada")
	}
	return nil
}

// SetAppKey sets or rotates the app public key. The IDE key is immutable here.
func (s *Store) SetAppKey(accountID, deviceID string, pub []byte) (Device, error) {
	if len(pub) != 32 {
		return Device{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "chave pública inválida"}
	}
	res, err := s.db.Exec(`UPDATE devices SET app_public_key=? WHERE id=? AND account_id=? AND revoked_at IS NULL`, pub, deviceID, accountID)
	if err != nil {
		return Device{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Device{}, errNotFound("IDE não encontrada")
	}
	return s.DeviceForAccount(accountID, deviceID)
}

// TouchDevice updates last_seen_at.
func (s *Store) TouchDevice(deviceID string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen_at=? WHERE id=?`, at.UTC().UnixMilli(), deviceID)
	return err
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 10)
	for i := range out {
		out[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out), nil
}

// CreateLinkCode stores a pending link. initiator is "ide" or "app".
func (s *Store) CreateLinkCode(accountID, initiator, name string, pub []byte) (LinkCode, error) {
	if initiator != relaywire.RoleIDE && initiator != relaywire.RoleApp {
		return LinkCode{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "role inválido"}
	}
	name = cleanName(name)
	if name == "" {
		name = "IDE"
	}
	if len(pub) != 32 {
		return LinkCode{}, &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "chave pública obrigatória"}
	}
	var code string
	var err error
	for i := 0; i < 5; i++ {
		code, err = newCode()
		if err != nil {
			return LinkCode{}, err
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM link_codes WHERE code=?`, code).Scan(&n); err != nil {
			return LinkCode{}, err
		}
		if n == 0 {
			break
		}
		code = ""
	}
	if code == "" {
		return LinkCode{}, errors.New("não foi possível gerar um código")
	}
	now := time.Now().UTC()
	lc := LinkCode{
		Code: code, AccountID: accountID, Initiator: initiator, Name: name,
		ExpiresAt: now.Add(linkTTL),
	}
	if initiator == relaywire.RoleIDE {
		lc.IdePublicKey = pub
	} else {
		lc.AppPublicKey = pub
	}
	_, err = s.db.Exec(`INSERT INTO link_codes(code, account_id, initiator, name, ide_public_key, app_public_key, expires_at, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		lc.Code, lc.AccountID, lc.Initiator, lc.Name, nullBytes(lc.IdePublicKey), nullBytes(lc.AppPublicKey), lc.ExpiresAt.UnixMilli(), now.UnixMilli())
	if err != nil {
		return LinkCode{}, err
	}
	return lc, nil
}

// GetLinkCode returns a code belonging to the account.
func (s *Store) GetLinkCode(accountID, code string) (LinkCode, error) {
	code = relaywire.NormalizeCode(code)
	row := s.db.QueryRow(`SELECT code, account_id, initiator, name, ide_public_key, app_public_key, device_id, device_token, expires_at, consumed_at FROM link_codes WHERE code=? AND account_id=?`, code, accountID)
	lc, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkCode{}, errNotFound("código não encontrado")
	}
	return lc, err
}

// ConfirmLink completes a code and creates the IDE. deviceToken is returned
// only when the confirmer is the IDE (initiator was the app). When the IDE
// initiated, the token stays on the row for a single poll.
func (s *Store) ConfirmLink(accountID, code, name string, pub []byte) (Device, string, error) {
	if len(pub) != 32 {
		return Device{}, "", &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "chave pública obrigatória"}
	}
	code = relaywire.NormalizeCode(code)
	tx, err := s.db.Begin()
	if err != nil {
		return Device{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRow(`SELECT code, account_id, initiator, name, ide_public_key, app_public_key, device_id, device_token, expires_at, consumed_at FROM link_codes WHERE code=? AND account_id=?`, code, accountID)
	lc, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, "", errNotFound("código não encontrado")
	}
	if err != nil {
		return Device{}, "", err
	}
	if !lc.ConsumedAt.IsZero() {
		return Device{}, "", &httpError{Status: 409, Code: relaywire.CodeConflict, Msg: "código já utilizado"}
	}
	if time.Now().UTC().After(lc.ExpiresAt) {
		return Device{}, "", &httpError{Status: 410, Code: relaywire.CodeExpired, Msg: "código expirado"}
	}
	if lc.Initiator == relaywire.RoleIDE {
		lc.AppPublicKey = pub
	} else {
		lc.IdePublicKey = pub
		if n := cleanName(name); n != "" {
			lc.Name = n
		}
	}
	dev, token, err := insertDeviceTx(tx, accountID, lc.Name, lc.IdePublicKey, lc.AppPublicKey)
	if err != nil {
		return Device{}, "", err
	}
	now := time.Now().UTC().UnixMilli()
	stored := ""
	returned := ""
	if lc.Initiator == relaywire.RoleIDE {
		stored = token
	} else {
		returned = token
	}
	if _, err := tx.Exec(`UPDATE link_codes SET ide_public_key=?, app_public_key=?, device_id=?, device_token=?, consumed_at=?, name=? WHERE code=?`,
		lc.IdePublicKey, lc.AppPublicKey, dev.ID, nullIfEmpty(stored), now, lc.Name, code); err != nil {
		return Device{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, "", err
	}
	return dev, returned, nil
}

// TakeLinkDeviceToken returns the one-time IDE credential for an IDE-initiated
// code and erases it. The app-facing poll must not call this.
func (s *Store) TakeLinkDeviceToken(accountID, code string) (string, error) {
	code = relaywire.NormalizeCode(code)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var initiator, token sql.NullString
	var consumed sql.NullInt64
	err = tx.QueryRow(`SELECT initiator, device_token, consumed_at FROM link_codes WHERE code=? AND account_id=?`, code, accountID).
		Scan(&initiator, &token, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound("código não encontrado")
	}
	if err != nil {
		return "", err
	}
	if !initiator.Valid || initiator.String != relaywire.RoleIDE || !token.Valid || token.String == "" {
		return "", nil
	}
	if _, err := tx.Exec(`UPDATE link_codes SET device_token=NULL WHERE code=?`, code); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token.String, nil
}

func insertDeviceTx(tx *sql.Tx, accountID, name string, idePub, appPub []byte) (Device, string, error) {
	id, err := newID("dev_")
	if err != nil {
		return Device{}, "", err
	}
	token, err := newSecret(relaywire.TokenDevicePrefix)
	if err != nil {
		return Device{}, "", err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`INSERT INTO devices(id, account_id, name, token_hash, ide_public_key, app_public_key, created_at) VALUES(?,?,?,?,?,?,?)`,
		id, accountID, name, tokenHash(token), idePub, appPub, now.UnixMilli()); err != nil {
		return Device{}, "", err
	}
	d := Device{
		ID: id, AccountID: accountID, Name: name,
		IdePublicKey: append([]byte(nil), idePub...),
		AppPublicKey: append([]byte(nil), appPub...),
		CreatedAt:    now,
	}
	return d, token, nil
}

// AppendEvent assigns the next sequence number. stamp is called inside the
// transaction and must return the envelope JSON that includes that sequence.
// Older rows beyond keep are deleted.
func (s *Store) AppendEvent(deviceID string, keep int, stamp func(seq int64) []byte) (int64, []byte, error) {
	if keep <= 0 {
		keep = eventKeep
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE devices SET event_seq = event_seq + 1 WHERE id=?`, deviceID); err != nil {
		return 0, nil, err
	}
	var seq int64
	if err := tx.QueryRow(`SELECT event_seq FROM devices WHERE id=?`, deviceID).Scan(&seq); err != nil {
		return 0, nil, err
	}
	body := stamp(seq)
	if len(body) == 0 {
		return 0, nil, errors.New("evento vazio")
	}
	if _, err := tx.Exec(`INSERT INTO event_log(device_id, seq, body, created_at) VALUES(?,?,?,?)`,
		deviceID, seq, body, time.Now().UTC().UnixMilli()); err != nil {
		return 0, nil, err
	}
	if seq > int64(keep) {
		if _, err := tx.Exec(`DELETE FROM event_log WHERE device_id=? AND seq<=?`, deviceID, seq-int64(keep)); err != nil {
			return 0, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return seq, body, nil
}

// EventsAfter returns events with seq greater than after, in order.
func (s *Store) EventsAfter(deviceID string, after int64, limit int) ([]StoredEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT seq, body FROM event_log WHERE device_id=? AND seq>? ORDER BY seq ASC LIMIT ?`, deviceID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var ev StoredEvent
		if err := rows.Scan(&ev.Seq, &ev.Body); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// EnqueueRPC stores an encrypted RPC until the IDE answers or it expires.
func (s *Store) EnqueueRPC(deviceID, accountID, id string, body []byte) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO rpc_queue(id, device_id, account_id, body, created_at) VALUES(?,?,?,?,?)`,
		id, deviceID, accountID, body, time.Now().UTC().UnixMilli())
	return err
}

// PullRPCs returns queued RPCs for a device and drops expired ones.
func (s *Store) PullRPCs(deviceID string) ([]QueuedRPC, error) {
	cutoff := time.Now().UTC().Add(-rpcTTL).UnixMilli()
	if _, err := s.db.Exec(`DELETE FROM rpc_queue WHERE created_at<?`, cutoff); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, body FROM rpc_queue WHERE device_id=? ORDER BY created_at ASC`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueuedRPC
	for rows.Next() {
		var q QueuedRPC
		if err := rows.Scan(&q.ID, &q.Body); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteRPC removes a queued call after the IDE answers.
func (s *Store) DeleteRPC(id string) error {
	_, err := s.db.Exec(`DELETE FROM rpc_queue WHERE id=?`, id)
	return err
}

// CreateQRTicket stores a hashed single-use QR login ticket in SQLite.
func (s *Store) CreateQRTicket(ticket, accountID string, expiresAt time.Time) error {
	if strings.TrimSpace(ticket) == "" || strings.TrimSpace(accountID) == "" {
		return &httpError{Status: 400, Code: relaywire.CodeInvalid, Msg: "ticket inválido"}
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO qr_tickets(ticket_hash, account_id, expires_at) VALUES(?,?,?)`,
		tokenHash(ticket), accountID, expiresAt.UTC().UnixMilli())
	return err
}

// RedeemQRTicket atomically looks up and removes a QR login ticket, rejecting expired ones.
func (s *Store) RedeemQRTicket(ticket string, now time.Time) (string, bool, error) {
	hash := tokenHash(ticket)
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	var accountID string
	var expMs int64
	err = tx.QueryRow(`SELECT account_id, expires_at FROM qr_tickets WHERE ticket_hash=?`, hash).Scan(&accountID, &expMs)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(`DELETE FROM qr_tickets WHERE ticket_hash=?`, hash); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	if now.UTC().After(time.UnixMilli(expMs).UTC()) {
		return "", false, nil
	}
	return accountID, true, nil
}

// Cleanup drops expired sessions, access tokens, link codes and denylist rows.
func (s *Store) Cleanup(now time.Time) error {
	ms := now.UTC().UnixMilli()
	stmts := []string{
		`DELETE FROM access_tokens WHERE expires_at < ` + fmt.Sprint(ms),
		`DELETE FROM refresh_denylist WHERE expires_at < ` + fmt.Sprint(ms),
		`DELETE FROM link_codes WHERE expires_at < ` + fmt.Sprint(ms) + ` AND consumed_at IS NULL`,
		`DELETE FROM rpc_queue WHERE created_at < ` + fmt.Sprint(now.Add(-rpcTTL).UnixMilli()),
		`DELETE FROM qr_tickets WHERE expires_at < ` + fmt.Sprint(ms),
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(row scanner) (Device, error) {
	var d Device
	var created, seen, revoked sql.NullInt64
	var ide, app []byte
	err := row.Scan(&d.ID, &d.AccountID, &d.Name, &ide, &app, &created, &seen, &revoked, &d.EventSeq)
	if err != nil {
		return Device{}, err
	}
	d.IdePublicKey = ide
	d.AppPublicKey = app
	if created.Valid {
		d.CreatedAt = time.UnixMilli(created.Int64).UTC()
	}
	if seen.Valid {
		d.LastSeenAt = time.UnixMilli(seen.Int64).UTC()
	}
	if revoked.Valid {
		d.RevokedAt = time.UnixMilli(revoked.Int64).UTC()
	}
	return d, nil
}

func scanLink(row scanner) (LinkCode, error) {
	var lc LinkCode
	var ide, app []byte
	var deviceID, token sql.NullString
	var exp int64
	var consumed sql.NullInt64
	err := row.Scan(&lc.Code, &lc.AccountID, &lc.Initiator, &lc.Name, &ide, &app, &deviceID, &token, &exp, &consumed)
	if err != nil {
		return LinkCode{}, err
	}
	lc.IdePublicKey = ide
	lc.AppPublicKey = app
	if deviceID.Valid {
		lc.DeviceID = deviceID.String
	}
	if token.Valid {
		lc.DeviceToken = token.String
	}
	lc.ExpiresAt = time.UnixMilli(exp).UTC()
	if consumed.Valid {
		lc.ConsumedAt = time.UnixMilli(consumed.Int64).UTC()
	}
	return lc, nil
}

func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		name = name[:64]
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}

func trimUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 200 {
		ua = ua[:200]
	}
	return ua
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type httpError struct {
	Status int
	Code   string
	Msg    string
}

func (e *httpError) Error() string { return e.Msg }

func errUnauthorized(msg string) error {
	return &httpError{Status: 401, Code: relaywire.CodeUnauthorized, Msg: msg}
}

func errNotFound(msg string) error {
	return &httpError{Status: 404, Code: relaywire.CodeNotFound, Msg: msg}
}

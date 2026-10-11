package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Accounts live in 629Manager (the 629fm identity service), not here. A
// browser on 629fm.com holds a Manager session token in localStorage; the game
// page POSTs it to /session, we resolve it with Manager and keep it in an
// HttpOnly cookie, and every authenticated request re-resolves it, so 629fm
// logouts and role changes apply immediately. Only `account` principals (not
// guests) may play. Our own DB just maps a principal to the int player ID the
// engine uses, plus game-only data (avatars).

//
// Types
//

type User struct {
	ID          int    `json:"id"`
	PrincipalID string `json:"-"`
	Username    string `json:"username"`
	IsAdmin     bool   `json:"is_admin"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// principal is the subset of Manager's Principal schema we use
// (services/manager/openapi/manager-v1.yaml).
type principal struct {
	PrincipalID string  `json:"principalId"`
	Type        string  `json:"type"` // guest | account
	Username    *string `json:"username"`
	Role        string  `json:"role"` // user | moderator | admin
}

type sessionIdentity struct {
	ExpiresAt time.Time `json:"expiresAt"`
	Principal principal `json:"principal"`
}

var (
	errNoSession      = errors.New("not logged in")
	errInvalidSession = errors.New("629fm session is invalid or expired; log in again")
	errNotAccount     = errors.New("only logged-in 629fm accounts can play")
)

//
// 629Manager
//

const sessionCookie = "session_token"

var (
	managerBaseURL = strings.TrimRight(envOr("MANAGER_BASE_URL", "http://manager:8081"), "/")
	managerKey     = os.Getenv("MANAGER_RAYCAST_SERVICE_KEY")
	managerHTTP    = &http.Client{Timeout: 5 * time.Second}
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveSession is Manager's getCurrentSession: the service key says who we
// are, the bearer token says who the player is.
func resolveSession(ctx context.Context, token string) (sessionIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, managerBaseURL+"/v1/sessions/current", nil)
	if err != nil {
		return sessionIdentity{}, err
	}
	req.Header.Set("X-Service-Key", managerKey)
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := managerHTTP.Do(req)
	if err != nil {
		return sessionIdentity{}, fmt.Errorf("manager: %w", err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized:
		return sessionIdentity{}, errInvalidSession
	case res.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return sessionIdentity{}, fmt.Errorf("manager: HTTP %d: %s", res.StatusCode, body)
	}

	var id sessionIdentity
	if err := json.NewDecoder(res.Body).Decode(&id); err != nil {
		return sessionIdentity{}, fmt.Errorf("manager: decoding session: %w", err)
	}
	return id, nil
}

// userFromToken resolves a Manager token to a player, creating the player row
// on first sight and keeping its username in step with the 629fm account.
func userFromToken(ctx context.Context, db *sql.DB, token string) (User, time.Time, error) {
	if token == "" {
		return User{}, time.Time{}, errNoSession
	}
	id, err := resolveSession(ctx, token)
	if err != nil {
		return User{}, time.Time{}, err
	}
	p := id.Principal
	if p.Type != "account" || p.Username == nil || *p.Username == "" {
		return User{}, time.Time{}, errNotAccount
	}

	user := User{PrincipalID: p.PrincipalID, Username: *p.Username, IsAdmin: p.Role == "admin"}
	err = db.QueryRowContext(ctx, `
		INSERT INTO players (principal_id, username) VALUES ($1, $2)
		ON CONFLICT (principal_id) DO UPDATE SET username = EXCLUDED.username
		RETURNING id
	`, user.PrincipalID, user.Username).Scan(&user.ID)
	if err != nil {
		return User{}, time.Time{}, fmt.Errorf("player upsert: %w", err)
	}
	return user, id.ExpiresAt, nil
}

func getUserBySessionCookie(db *sql.DB, r *http.Request) (User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return User{}, errNoSession
	}
	user, _, err := userFromToken(r.Context(), db, c.Value)
	return user, err
}

// writeAuthError maps an auth failure to a response; Manager outages are
// logged and reported as 503 rather than as "not logged in".
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSession), errors.Is(err, errInvalidSession):
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: err.Error()})
	case errors.Is(err, errNotAccount):
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: err.Error()})
	default:
		log.Println("auth:", err)
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "629fm accounts are unavailable, try again later"})
	}
}

//
// Helpers
//

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Println("json encode error:", err)
	}
}

func readJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

//
// DB interactions
//

func connectPostgresDB() *sql.DB {
	// allow overriding the database URL
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		connString = "user=myuser dbname=myapp password=strongpassword host=localhost port=5432 sslmode=disable"
	}
	log.Println("connecting to the database at: ", connString)

	db, err := sql.Open("postgres", connString)
	if err != nil {
		log.Fatal("sql.Open error:", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatal("db.Ping error:", err)
	}

	fmt.Println("connected to postgres")
	return db
}

func updateAvatar(db *sql.DB, playerID int, avatar []byte, mimeType, filename string) error {
	_, err := db.Exec(
		`UPDATE players
		 SET avatar = $1, avatar_mime = $2, avatar_filename = $3
		 WHERE id = $4`,
		avatar, mimeType, filename, playerID,
	)
	return err
}

func getAvatar(db *sql.DB, playerID int) ([]byte, string, string, error) {
	var data []byte
	var mimeType sql.NullString
	var filename sql.NullString

	err := db.QueryRow(
		`SELECT avatar, avatar_mime, avatar_filename
		 FROM players
		 WHERE id = $1`,
		playerID,
	).Scan(&data, &mimeType, &filename)

	if err != nil {
		return nil, "", "", err
	}

	return data, mimeType.String, filename.String, nil
}

//
// Handlers
//

// sessionHandle trades the browser's Manager token (Authorization: Bearer)
// for the game's session cookie. Pages call it on every load, so the cookie
// always follows whichever 629fm account the browser is currently logged in as.
func sessionHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeAuthError(w, errNoSession)
		return
	}
	token = strings.TrimSpace(token)
	user, expires, err := userFromToken(r.Context(), db, token)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: token,
		// No Path: the browser defaults it to this request's directory, which
		// is /raycast behind the 629fm proxy and / when run standalone.
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, http.StatusOK, user)
}

func uploadAvatarHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 5<<20) // 5 MB max

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid multipart form or file too large"})
		return
	}

	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	file, header, err := r.FormFile("avatar")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "missing avatar file"})
		return
	}
	defer file.Close()

	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "failed to read file"})
		return
	}

	mimeType := http.DetectContentType(sniff[:n])
	if mimeType != "image/png" && mimeType != "image/gif" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "only PNG and GIF are allowed"})
		return
	}

	if _, err := file.Seek(0, 0); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to reset file reader"})
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to read avatar"})
		return
	}

	if len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "empty file"})
		return
	}

	if err := updateAvatar(db, user.ID, data, mimeType, header.Filename); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to save avatar"})
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("avatar uploaded for %s", user.Username),
	})
}

func avatarHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use GET"})
		return
	}

	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	data, mimeType, filename, err := getAvatar(db, user.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "user not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to fetch avatar"})
		return
	}

	if len(data) == 0 {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "avatar not set"})
		return
	}

	w.Header().Set("Content-Type", mimeType)
	if filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, filename))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

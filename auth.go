package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"net/http"

	_ "github.com/lib/pq"
)

//
// Types
//

type User struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

//
// Helpers
//

func getSessionToken(r *http.Request) (string, error) {
	c, err := r.Cookie("session_token")
	if err != nil {
		return "", err
	}
	return c.Value, nil
}

func getUserBySessionCookie(db *sql.DB, r *http.Request) (User, error) {
	token, err := getSessionToken(r)
	if err != nil {
		return User{}, err
	}

	var user User
	err = db.QueryRow(`
		SELECT u.id, u.username, u.password
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = $1
	`, token).Scan(&user.ID, &user.Username, &user.Password)
	if err != nil {
		return User{}, err
	}

	return user, nil
}

func newToken64() (string, error) {
	b := make([]byte, 8) // 8 bytes = 64 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil // 16 hex chars
}

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

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func checkPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword(
		[]byte(hash),
		[]byte(password),
	)
	return err == nil
}

//
// DB interactions
//

func connectPostgresDB() *sql.DB {
	connString := "user=myuser dbname=myapp password=strongpassword host=127.0.0.1 port=5432 sslmode=disable"

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

func insertSession(db *sql.DB, user User, session_token string) error {
	_, err := db.Exec(
		"INSERT INTO sessions(user_id, token) VALUES($1, $2)",
		user.ID,
		session_token,
	)
	if err != nil {
		log.Println("session insert error:", err)
		return err
	}

	fmt.Printf("session for user %s inserted\n", user.Username)
	return err
}

func terminateSession(db *sql.DB, session_token string) error {
	_, err := db.Exec(
		"DELETE FROM sessions WHERE token=$1",
		session_token,
	)
	if err != nil {
		log.Println("session insert error:", err)
		return err
	}

	fmt.Printf("session terminated\n")
	return err
}

func insertUser(db *sql.DB, user User) error {
	hashedPassword, err := hashPassword(user.Password)
	if err != nil {
		log.Println("password hashing error:", err)
		return err
	}

	_, err = db.Exec(
		"INSERT INTO users(username, password) VALUES($1, $2)",
		user.Username,
		hashedPassword,
	)
	if err != nil {
		log.Println("insert error:", err)
		return err
	}

	fmt.Printf("user %s inserted\n", user.Username)
	return err
}

func updatePassword(db *sql.DB, username, newPassword string) error {
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		"UPDATE users SET password=$1 WHERE username=$2",
		hash, username,
	)
	return err
}

func deleteUser(db *sql.DB, username string) error {
	_, err := db.Exec(
		"DELETE FROM users WHERE username=$1",
		username,
	)
	if err != nil {
		log.Println("deleting error:", err)
		return err
	}

	fmt.Printf("user %s deleted\n", username)

	return nil
}

func getUserByUsername(db *sql.DB, username string) (User, error) {
	var user User

	err := db.QueryRow(
		"SELECT id, username, password FROM users WHERE username = $1",
		username,
	).Scan(&user.ID, &user.Username, &user.Password)

	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Println("no user found")
			return user, err
		}
		fmt.Println("query error:", err)
		return user, err
	}

	return user, err
}

func getUserByID(db *sql.DB, user_id int) (User, error) {
	var user User

	err := db.QueryRow(
		"SELECT id, username, password FROM users WHERE id = $1",
		user_id,
	).Scan(&user.ID, &user.Username, &user.Password)

	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Println("no user found")
			return user, err
		}
		fmt.Println("query error:", err)
		return user, err
	}

	return user, err
}

func updateAvatar(db *sql.DB, username string, avatar []byte, mimeType, filename string) error {
	_, err := db.Exec(
		`UPDATE users
		 SET avatar = $1, avatar_mime = $2, avatar_filename = $3
		 WHERE username = $4`,
		avatar, mimeType, filename, username,
	)
	return err
}

func getAvatar(db *sql.DB, username string) ([]byte, string, string, error) {
	var data []byte
	var mimeType sql.NullString
	var filename sql.NullString

	err := db.QueryRow(
		`SELECT avatar, avatar_mime, avatar_filename
		 FROM users
		 WHERE username = $1`,
		username,
	).Scan(&data, &mimeType, &filename)

	if err != nil {
		return nil, "", "", err
	}

	return data, mimeType.String, filename.String, nil
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
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
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

	if err := updateAvatar(db, user.Username, data, mimeType, header.Filename); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to save avatar"})
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("avatar uploaded for %s", user.Username),
	})
}

func meHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use GET"})
		return
	}

	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("you are logged in as: %s", user.Username),
	})
}

func avatarHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use GET"})
		return
	}

	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
		return
	}

	data, mimeType, filename, err := getAvatar(db, user.Username)
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

func registerHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	defer r.Body.Close()

	var creds Credentials
	if err := readJSON(r, &creds); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid json"})
		return
	}

	if creds.Username == "" || creds.Password == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "username and password are required"})
		return
	}

	user := User{
		Username: creds.Username,
		Password: creds.Password,
	}

	if err := insertUser(db, user); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "registration failed"})
		return
	}

	writeJSON(w, http.StatusCreated, MessageResponse{
		Message: fmt.Sprintf("registration successful; created user %s", user.Username),
	})
}

func loginHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	defer r.Body.Close()

	var creds Credentials
	if err := readJSON(r, &creds); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid json"})
		return
	}

	if creds.Username == "" || creds.Password == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "username and password are required"})
		return
	}

	user, err := getUserByUsername(db, creds.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "invalid username or password"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "user fetching failed"})
		return
	}

	if !checkPassword(user.Password, creds.Password) {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "invalid username or password"})
		return
	}

	token, err := newToken64()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "user token generation failed"})
		return
	}

	if err := insertSession(db, user, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "session creation failed"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    token,
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7, // 7 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // true in production with HTTPS
	})

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("login successful; welcome %s", user.Username),
	})

}

func logoutHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	defer r.Body.Close()

	token, err := getSessionToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
		return
	}

	if err := terminateSession(db, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "session creation failed"})
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("logged out successfully"),
	})
}

func unregisterHandle(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}

	defer r.Body.Close()

	var creds Credentials
	if err := readJSON(r, &creds); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid json"})
		return
	}

	if creds.Username == "" || creds.Password == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "username and password are required"})
		return
	}

	user, err := getUserByUsername(db, creds.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "invalid username or password"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "user fetching failed"})
		return
	}

	if !checkPassword(user.Password, creds.Password) {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "invalid username or password"})
		return
	}

	if err := deleteUser(db, creds.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "unregister failed"})
		return
	}

	writeJSON(w, http.StatusOK, MessageResponse{
		Message: fmt.Sprintf("user %s unregistered", user.Username),
	})
}

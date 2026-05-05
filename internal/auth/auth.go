package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

const defaultIterations = 200_000
const keyLen = 32 // SHA-256 output length

// AllPlatforms is the full list of supported platforms.
var AllPlatforms = []string{"youtube", "twitter", "tiktok", "instagram"}

// PasswordRecord holds PBKDF2-SHA256 hash components.
// JSON field names match Python's auth_utils.py for cross-compatibility.
type PasswordRecord struct {
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
}

// UserRecord represents a user entry in the auth_users.json file.
// Supports both the Go nested format ({"password":{...},"role":...})
// and the legacy Python flat format ({"hash":...,"salt":...,"iterations":...}).
type UserRecord struct {
	Password  PasswordRecord `json:"password"`
	Role      string         `json:"role"`
	Platforms []string       `json:"platforms"`
}

func (u *UserRecord) UnmarshalJSON(data []byte) error {
	// Try nested format first.
	type plain UserRecord
	var nested plain
	if err := json.Unmarshal(data, &nested); err != nil {
		return err
	}
	*u = UserRecord(nested)

	// If password fields are empty, try flat format (Python legacy).
	if u.Password.Hash == "" {
		var flat struct {
			Hash       string   `json:"hash"`
			Salt       string   `json:"salt"`
			Iterations int      `json:"iterations"`
			Role       string   `json:"role"`
			Platforms  []string `json:"platforms"`
		}
		if err := json.Unmarshal(data, &flat); err == nil && flat.Hash != "" {
			u.Password = PasswordRecord{
				Hash:       flat.Hash,
				Salt:       flat.Salt,
				Iterations: flat.Iterations,
			}
			if flat.Role != "" {
				u.Role = flat.Role
			}
			if flat.Platforms != nil {
				u.Platforms = flat.Platforms
			}
		}
	}

	// Default role and platforms if missing.
	if u.Role == "" {
		u.Role = "admin"
	}
	if u.Platforms == nil {
		u.Platforms = AllPlatforms
	}
	return nil
}

// HashPassword generates a new PBKDF2-SHA256 hash for the given password.
// 16-byte random salt, 200K iterations, base64-encoded salt and hash.
func HashPassword(password string) PasswordRecord {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(fmt.Sprintf("auth: rand.Read: %v", err))
	}
	dk := pbkdf2.Key([]byte(password), salt, defaultIterations, keyLen, sha256.New)
	return PasswordRecord{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Hash:       base64.StdEncoding.EncodeToString(dk),
		Iterations: defaultIterations,
	}
}

// VerifyPassword checks whether password matches the stored record.
func VerifyPassword(password string, record PasswordRecord) bool {
	salt, err := base64.StdEncoding.DecodeString(record.Salt)
	if err != nil {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(record.Hash)
	if err != nil {
		return false
	}
	iterations := record.Iterations
	if iterations <= 0 {
		iterations = defaultIterations
	}
	dk := pbkdf2.Key([]byte(password), salt, iterations, keyLen, sha256.New)
	return hmac.Equal(dk, expected)
}

// LoadUsers reads the auth_users.json file. Returns empty map if file is missing.
func LoadUsers(path string) (map[string]UserRecord, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]UserRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read auth users: %w", err)
	}
	var users map[string]UserRecord
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, fmt.Errorf("parse auth users: %w", err)
	}
	return users, nil
}

// SaveUsers writes users to path using an atomic temp-file + rename pattern.
// The file is created with 0600 permissions.
func SaveUsers(path string, users map[string]UserRecord) error {
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth users: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir auth dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".auth_users_*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write auth users: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod auth users: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename auth users: %w", err)
	}
	return nil
}

// --- In-memory user cache ---

var (
	cacheMu   sync.RWMutex
	cachePath string
	cacheData map[string]UserRecord

	// fileMu serializes load-modify-save cycles so concurrent admin requests
	// don't race on the auth_users.json file.
	fileMu sync.Mutex
)

// LockUsers acquires the file-level mutex. Callers must call UnlockUsers when done.
// Use to wrap the LoadUsers → modify → SaveUsers sequence in handlers.
func LockUsers() { fileMu.Lock() }

// UnlockUsers releases the file-level mutex.
func UnlockUsers() { fileMu.Unlock() }

// InitCache initializes the in-memory user cache from disk.
// Call once at startup with the auth_users.json path.
func InitCache(path string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachePath = path
	cacheData, _ = LoadUsers(path)
	if cacheData == nil {
		cacheData = map[string]UserRecord{}
	}
}

// GetCachedUsers returns the current cached copy of all users.
func GetCachedUsers() map[string]UserRecord {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	// Return a shallow copy so callers can't mutate the cache.
	out := make(map[string]UserRecord, len(cacheData))
	for k, v := range cacheData {
		out[k] = v
	}
	return out
}

// InvalidateCache reloads the user cache from disk.
func InvalidateCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachePath == "" {
		return
	}
	users, _ := LoadUsers(cachePath)
	if users == nil {
		users = map[string]UserRecord{}
	}
	cacheData = users
}

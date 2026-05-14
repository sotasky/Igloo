package auth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	rec := HashPassword("correct-horse-battery-staple")

	if !VerifyPassword("correct-horse-battery-staple", rec) {
		t.Error("VerifyPassword: correct password should return true")
	}
	if VerifyPassword("wrong-password", rec) {
		t.Error("VerifyPassword: wrong password should return false")
	}
}

func TestHashCompatibility(t *testing.T) {
	rec := HashPassword("test-password")
	if rec.Algorithm != passwordAlgorithmArgon2id {
		t.Errorf("algorithm: got %q, want %q", rec.Algorithm, passwordAlgorithmArgon2id)
	}

	// Salt should be base64 of 16 bytes = 24 chars
	salt, err := base64.StdEncoding.DecodeString(rec.Salt)
	if err != nil {
		t.Fatalf("salt is not valid base64: %v", err)
	}
	if len(salt) != defaultSaltLen {
		t.Errorf("salt length: got %d, want %d", len(salt), defaultSaltLen)
	}

	// Hash should be base64 of 32 bytes = 44 chars.
	hash, err := base64.StdEncoding.DecodeString(rec.Hash)
	if err != nil {
		t.Fatalf("hash is not valid base64: %v", err)
	}
	if len(hash) != 32 {
		t.Errorf("hash length: got %d, want 32", len(hash))
	}

	if rec.MemoryKiB != defaultArgon2idMemoryKiB {
		t.Errorf("memory_kib: got %d, want %d", rec.MemoryKiB, defaultArgon2idMemoryKiB)
	}
	if rec.Time != defaultArgon2idTime {
		t.Errorf("time: got %d, want %d", rec.Time, defaultArgon2idTime)
	}
	if rec.Parallelism != defaultArgon2idParallelism {
		t.Errorf("parallelism: got %d, want %d", rec.Parallelism, defaultArgon2idParallelism)
	}
	if PasswordNeedsRehash(rec) {
		t.Error("fresh Argon2id record should not need rehash")
	}
}

func TestPBKDF2Compatibility(t *testing.T) {
	rec := hashPasswordPBKDF2("test-password", defaultIterations)
	if !VerifyPassword("test-password", rec) {
		t.Fatal("explicit PBKDF2 record did not verify")
	}
	if VerifyPassword("wrong-password", rec) {
		t.Fatal("wrong password verified against PBKDF2 record")
	}
	if !PasswordNeedsRehash(rec) {
		t.Fatal("PBKDF2 record should need rehash")
	}

	rec.Algorithm = ""
	if !VerifyPassword("test-password", rec) {
		t.Fatal("legacy implicit PBKDF2 record did not verify")
	}
	if !PasswordNeedsRehash(rec) {
		t.Fatal("legacy implicit PBKDF2 record should need rehash")
	}
}

func TestLoadSaveUsers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth_users.json")

	users := map[string]UserRecord{
		"user_a": {
			Password:  HashPassword("secret1"),
			Role:      "admin",
			Platforms: []string{"youtube", "twitter"},
		},
		"user_b": {
			Password:  HashPassword("secret2"),
			Role:      "viewer",
			Platforms: []string{"youtube"},
		},
	}

	if err := SaveUsers(path, users); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file permissions: got %o, want 600", perm)
	}

	// Round-trip load
	loaded, err := LoadUsers(path)
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded user count: got %d, want 2", len(loaded))
	}

	// Verify passwords are still valid after round-trip
	if !VerifyPassword("secret1", loaded["user_a"].Password) {
		t.Error("user_a password verification failed after round-trip")
	}
	if !VerifyPassword("secret2", loaded["user_b"].Password) {
		t.Error("user_b password verification failed after round-trip")
	}
	if loaded["user_a"].Role != "admin" {
		t.Errorf("user_a role: got %q, want %q", loaded["user_a"].Role, "admin")
	}
}

func TestLoadUsersMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	users, err := LoadUsers(path)
	if err != nil {
		t.Fatalf("LoadUsers on missing file should not error: %v", err)
	}
	if users == nil {
		t.Fatal("LoadUsers on missing file should return empty map, not nil")
	}
	if len(users) != 0 {
		t.Errorf("expected empty map, got %d entries", len(users))
	}
}

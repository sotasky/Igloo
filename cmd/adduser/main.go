// cmd/adduser creates or replaces a user in auth_users.json.
// Usage: go run ./cmd/adduser -username NAME -password PASS
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
)

func main() {
	username := flag.String("username", "", "username to create")
	password := flag.String("password", "", "password for the user")
	role := flag.String("role", "admin", "user role")
	platformsFlag := flag.String("platforms", "", "comma-separated platform access; defaults to IGLOO_ENABLED_PLATFORMS")
	flag.Parse()

	if *username == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: adduser -username NAME -password PASS [-role ROLE] [-platforms youtube,twitter,tiktok,instagram]")
		os.Exit(1)
	}

	cfg := config.Load()
	if cfg.ConfigError != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", cfg.ConfigError)
		os.Exit(1)
	}
	platforms := cfg.EnabledPlatforms
	if *platformsFlag != "" {
		parsed, err := config.ParseEnabledPlatforms(*platformsFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid platforms: %v\n", err)
			os.Exit(1)
		}
		platforms = cfg.EffectivePlatforms(parsed)
		if len(platforms) == 0 {
			fmt.Fprintln(os.Stderr, "invalid platforms: none are enabled by IGLOO_ENABLED_PLATFORMS")
			os.Exit(1)
		}
	}

	auth.LockUsers()
	defer auth.UnlockUsers()

	users, err := auth.LoadUsers(cfg.AuthUsersPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load users: %v\n", err)
		os.Exit(1)
	}

	users[*username] = auth.UserRecord{
		Password:  auth.HashPassword(*password),
		Role:      *role,
		Platforms: platforms,
	}

	if err := auth.SaveUsers(cfg.AuthUsersPath, users); err != nil {
		fmt.Fprintf(os.Stderr, "save users: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("user %q saved to %s\n", *username, cfg.AuthUsersPath)
}

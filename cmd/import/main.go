// cmd/import imports a current Igloo full export zip into a local install.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/fullimport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("igloo-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	replace := fs.Bool("replace", false, "replace existing settings, bookmarks, and bookmark categories for the selected owner before importing")
	user := fs.String("user", "", "import user-owned rows for this username; defaults to the exported owner, the only configured user, or bootstrap before first setup")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: igloo-import [--replace] [--user USERNAME] igloo-full-*.zip")
		return 2
	}

	cfg := config.Load()
	if cfg.ConfigError != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", cfg.ConfigError)
		return 1
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "create data dir: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(cfg.ConfDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "create config dir: %v\n", err)
		return 1
	}

	zipPath := fs.Arg(0)
	data, err := os.ReadFile(zipPath)
	if err != nil {
		fmt.Fprintf(stderr, "read export zip: %v\n", err)
		return 1
	}
	if !fullimport.IsZipPayload(data) {
		fmt.Fprintln(stderr, "not an Igloo full export zip: missing zip signature")
		return 1
	}
	exportCfg, err := fullimport.ReadExportConfig(data)
	if err != nil {
		fmt.Fprintf(stderr, "read full export manifest: %v\n", err)
		return 1
	}

	owner, ownerLabel, err := resolveImportOwner(cfg.AuthUsersPath, *user, exportCfg.UserID)
	if err != nil {
		fmt.Fprintf(stderr, "resolve owner: %v\n", err)
		return 1
	}

	store, err := db.Open(cfg.DatabasePath, cfg.DataDir)
	if err != nil {
		fmt.Fprintf(stderr, "open database: %v\n", err)
		return 1
	}
	defer store.Close()

	result, restoredMedia, restoredConfig, err := fullimport.ImportFullExportZip(store, cfg.DataDir, cfg.ConfDir, cfg.RepoDir, data, owner, *replace)
	if err != nil {
		fmt.Fprintf(stderr, "import full export: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "format=full_export_zip")
	fmt.Fprintf(stdout, "owner=%s\n", ownerLabel)
	fmt.Fprintf(stdout, "data_dir=%s\n", cfg.DataDir)
	fmt.Fprintf(stdout, "config_dir=%s\n", cfg.ConfDir)
	fmt.Fprintf(stdout, "database=%s\n", cfg.DatabasePath)
	fmt.Fprintf(stdout, "replace=%t\n", *replace)
	fmt.Fprintf(stdout, "added_channels=%d added_bookmarks=%d added_categories=%d updated_settings=%d restored_media=%d restored_config_files=%d skipped=%d\n",
		result.AddedChannels, result.AddedBookmarks, result.AddedCategories, result.UpdatedSettings, restoredMedia, restoredConfig, result.Skipped)
	return 0
}

func resolveImportOwner(authUsersPath, explicit, exported string) (userID, label string, err error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, "user:" + explicit, nil
	}
	exported = strings.TrimSpace(exported)
	if exported != "" {
		return exported, "user:" + exported, nil
	}

	users, err := auth.LoadUsers(authUsersPath)
	if err != nil {
		return "", "", err
	}
	switch len(users) {
	case 0:
		return "", "bootstrap", nil
	case 1:
		names := make([]string, 0, len(users))
		for name := range users {
			names = append(names, name)
		}
		return names[0], "user:" + names[0], nil
	default:
		names := make([]string, 0, len(users))
		for name := range users {
			names = append(names, name)
		}
		sort.Strings(names)
		return "", "", fmt.Errorf("multiple users configured (%s); pass --user", strings.Join(names, ", "))
	}
}

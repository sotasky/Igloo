// cmd/import imports a current Igloo full export zip into a local install.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/restore"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("igloo-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: igloo-import igloo-full-*.zip")
		return 2
	}

	cfg := config.Load()
	if err := cfg.Storage.Ensure(); err != nil {
		_, _ = fmt.Fprintf(stderr, "validate storage: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(cfg.ConfDir, 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "create config dir: %v\n", err)
		return 1
	}

	zipPath := fs.Arg(0)
	file, err := os.Open(zipPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "open export zip: %v\n", err)
		return 1
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		_, _ = fmt.Fprintf(stderr, "stat export zip: %v\n", statErr)
		return 1
	}
	stageErr := restore.StageZip(file, info.Size(), cfg.Storage)
	closeErr := file.Close()
	if stageErr != nil || closeErr != nil {
		if stageErr == nil {
			stageErr = closeErr
		}
		_, _ = fmt.Fprintf(stderr, "restore full export backup: %v\n", stageErr)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "format=zip_backup")
	_, _ = fmt.Fprintln(stdout, "pending=true")
	_, _ = fmt.Fprintf(stdout, "data_dir=%s\n", cfg.Storage.StateRoot())
	_, _ = fmt.Fprintf(stdout, "config_dir=%s\n", cfg.ConfDir)
	_, _ = fmt.Fprintf(stdout, "database=%s\n", cfg.Storage.DatabasePath())
	return 0
}

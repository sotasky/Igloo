package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/screwys/igloo/internal/devtools/androidsyncmaintenance"
	"github.com/screwys/igloo/internal/devtools/assetinventoryreconcile"
	"github.com/screwys/igloo/internal/devtools/lifecycleaudit"
	"github.com/screwys/igloo/internal/devtools/persistenceaudit"
	"github.com/screwys/igloo/internal/devtools/queryaudit"
	"github.com/screwys/igloo/internal/devtools/sqliterepack"
	"github.com/screwys/igloo/internal/devtools/storagemaintenance"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "android-sync-maintenance":
		return androidsyncmaintenance.Run(rest, stdout, stderr)
	case "asset-inventory-reconcile":
		return assetinventoryreconcile.Run(rest, stdout, stderr)
	case "lifecycle-audit":
		return lifecycleaudit.Run(rest, stdout, stderr)
	case "persistence-audit":
		return persistenceaudit.Run(rest, stdout, stderr)
	case "query-audit":
		return queryaudit.Run(rest, stdout, stderr)
	case "sqlite-repack":
		return sqliterepack.Run(rest, stdout, stderr)
	case "storage-maintenance":
		return storagemaintenance.Run(rest, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "igloo-dev: unknown command %q\n\n", command)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`Usage: igloo-dev <command> [args]

Commands:
  android-sync-maintenance    Drain bounded Android sync derived-cache debt
  asset-inventory-reconcile   Audit or repair missing asset inventory rows
  lifecycle-audit             Scan destructive SQL and table lifecycles
  persistence-audit           Group database size and rows by schema lifecycle
  query-audit                 Time and explain SQLite hot-path reads
  sqlite-repack               Report or create a compact SQLite database copy
  storage-maintenance         Audit or apply storage dedupe, retention, and asset-state repairs
`)+"\n")
}

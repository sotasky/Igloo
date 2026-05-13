package db

func schemaCreateStatements() []string {
	groups := [][]string{
		schemaArchiveStatements(),
		schemaUserStateStatements(),
		schemaLegacyMigrationStatements(),
		schemaDiagnosticStatements(),
		schemaMaintainedStateStatements(),
		schemaDerivedCacheStatements(),
		schemaSearchStatements(),
		schemaQueueStatements(),
		schemaSecurityStateStatements(),
	}

	total := 0
	for _, group := range groups {
		total += len(group)
	}
	stmts := make([]string, 0, total)
	for _, group := range groups {
		stmts = append(stmts, group...)
	}
	return stmts
}

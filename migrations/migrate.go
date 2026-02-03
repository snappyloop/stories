package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

//go:embed *.sql
var fs embed.FS

const schemaTable = `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY);`

// Run runs all pending migrations in order. Safe to call on every startup.
func Run(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if _, err := db.ExecContext(ctx, schemaTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		var applied bool
		err := db.QueryRowContext(ctx, `SELECT true FROM schema_migrations WHERE version = $1`, version).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %s: %w", version, err)
		}

		body, err := fs.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		log.Info().Str("version", version).Msg("Running migration")
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("run %s: %w", version, err)
		}

		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}

	return nil
}

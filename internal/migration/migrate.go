// Package migration manages PostgreSQL-specific DDL that GORM cannot express.
//
// Migrations are split into two phases:
//   - Pre  (before GORM AutoMigrate): extensions that must exist before table creation.
//   - Post (after GORM AutoMigrate):  tsvector columns, GIN indexes, schema fixes.
//
// All SQL statements are idempotent (IF NOT EXISTS / IF EXISTS guards) so they
// are safe to run on every startup.
package migration

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"

	"gorm.io/gorm"
)

//go:embed pre/*.sql
var preFS embed.FS

//go:embed post/*.sql
var postFS embed.FS

// RunPre executes pre-GORM migrations (extensions).
func RunPre(db *gorm.DB) error {
	return runFS(db, preFS, "pre")
}

// RunPost executes post-GORM migrations (PG-specific columns, GIN indexes, fixes).
func RunPost(db *gorm.DB) error {
	return runFS(db, postFS, "post")
}

func runFS(db *gorm.DB, efs embed.FS, dir string) error {
	entries, err := fs.ReadDir(efs, dir)
	if err != nil {
		return fmt.Errorf("read migration dir %s: %w", dir, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		content, err := fs.ReadFile(efs, dir+"/"+e.Name())
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}

		if err := db.Exec(string(content)).Error; err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}

		log.Printf("[migration] OK: %s", e.Name())
	}

	return nil
}

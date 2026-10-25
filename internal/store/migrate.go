package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations 从任意 fs 读取 migrations/*.sql，按文件名前缀版本号升序返回。
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("读取 migrations 目录: %w", err)
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		head, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("迁移文件名 %q 缺少 `_` 分隔的版本号", e.Name())
		}
		version, err := strconv.Atoi(head)
		if err != nil {
			return nil, fmt.Errorf("迁移文件名 %q 的版本号不是整数: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: version, Name: e.Name(), SQL: string(body)})
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	return out, nil
}

// Migrate 建 schema_migrations 版本表并逐版本应用未执行过的迁移，每个迁移一个事务。
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 schema_migrations: %w", err)
	}
	migs, err := LoadMigrations(migrationFS)
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("读取当前迁移版本: %w", err)
	}
	for _, m := range migs {
		if m.Version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("应用迁移 %s: %w", m.Name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (?, ?, strftime('%s','now') * 1000)`, m.Version, m.Name); err != nil {
		return err
	}
	return tx.Commit()
}

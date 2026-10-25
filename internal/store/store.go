// Package store 是 Veyra 的 SQLite 访问层。
//
// 沿用 ciryanna-account 的存储骨架：modernc.org/sqlite（纯 Go、CGO_ENABLED=0
// 静态编译）+ WAL + 手写 SQL + embed migrations。所有表操作收敛在本包，
// 上层（httpapi/cli）不直接碰 *sql.DB。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // 驱动名 "sqlite"
)

// querier 让同一套数据方法既能跑在 *sql.DB 上，也能跑在 *sql.Tx 上。
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db *sql.DB
	q  querier
	tx *sql.Tx
}

// Open 打开（必要时创建）SQLite 文件并配置 WAL；不自动迁移，由调用方显式 Migrate。
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("创建数据库目录 %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", buildDSN(path))
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 SQLite %s: %w", path, err)
	}
	return &Store{db: db, q: db}, nil
}

// buildDSN 组 WAL 单写者所需的 pragma。
// _txlock=immediate 是必需的：默认 deferred 事务在升级写锁时 WAL 下直接
// 返回 SQLITE_BUSY 而不等 busy_timeout，写入会随机失败。
func buildDSN(path string) string {
	p := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23", " ", "%20").
		Replace(filepath.ToSlash(path))
	return "file:" + p + "?" + strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
		"_txlock=immediate",
	}, "&")
}

func (s *Store) Migrate(ctx context.Context) error { return Migrate(ctx, s.db) }

// WithinTx 在单事务内执行 fn。已在事务中时直接复用，允许嵌套。
// fn 返回任何 error 都会回滚整个事务。
func (s *Store) WithinTx(ctx context.Context, fn func(*Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	inner := &Store{db: s.db, q: tx, tx: tx}
	if err := fn(inner); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Ping 供健康检查：真跑一次 SELECT 1。
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.q.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrVKeyNotFound = errors.New("虚拟 key 不存在或已禁用")

// VKey 是发给应用的访问凭证。明文 key 只在创建时返回一次，
// 库里只存 SHA-256 哈希与显示用前缀。
type VKey struct {
	ID         int64
	AppName    string
	KeyHash    string
	KeyPrefix  string
	Enabled    bool
	CreatedAt  int64
	LastUsedAt int64 // 0 = 从未使用
}

// GenerateVKey 生成 192 bit 随机 key，格式 vk-<48 hex>。
// 熵足够大，哈希无需带密钥（对比 ciryanna-account 的 6 位验证码必须 HMAC）。
func GenerateVKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机数: %w", err)
	}
	return "vk-" + hex.EncodeToString(b), nil
}

func HashVKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateVKey 为应用签发新 key。同名应用重复签发会直接替换旧 key
// （旧 key 立即失效）——CLI 语义是"重置凭证"，不是"多 key 并存"。
func (s *Store) CreateVKey(ctx context.Context, appName string) (plaintext string, v VKey, err error) {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return "", VKey{}, fmt.Errorf("应用名不能为空")
	}
	plaintext, err = GenerateVKey()
	if err != nil {
		return "", VKey{}, err
	}
	now := time.Now().UnixMilli()
	v = VKey{
		AppName:   appName,
		KeyHash:   HashVKey(plaintext),
		KeyPrefix: plaintext[:12],
		Enabled:   true,
		CreatedAt: now,
	}
	err = s.q.QueryRowContext(ctx, `
		INSERT INTO vkeys (app_name, key_hash, key_prefix, enabled, created_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(app_name) DO UPDATE SET
			key_hash = excluded.key_hash,
			key_prefix = excluded.key_prefix,
			enabled = 1,
			created_at = excluded.created_at,
			last_used_at = NULL
		RETURNING id`,
		appName, v.KeyHash, v.KeyPrefix, now,
	).Scan(&v.ID)
	if err != nil {
		return "", VKey{}, fmt.Errorf("签发 vkey %s: %w", appName, err)
	}
	return plaintext, v, nil
}

// FindVKeyByHash 是认证热路径。不存在与禁用同错，避免泄露应用清单。
func (s *Store) FindVKeyByHash(ctx context.Context, keyHash string) (VKey, error) {
	var v VKey
	err := s.q.QueryRowContext(ctx, `
		SELECT id, app_name, key_hash, key_prefix, enabled, created_at, COALESCE(last_used_at, 0)
		FROM vkeys WHERE key_hash = ? AND enabled = 1`, keyHash,
	).Scan(&v.ID, &v.AppName, &v.KeyHash, &v.KeyPrefix, &v.Enabled, &v.CreatedAt, &v.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VKey{}, ErrVKeyNotFound
	}
	if err != nil {
		return VKey{}, fmt.Errorf("查 vkey: %w", err)
	}
	return v, nil
}

// TouchLastUsed 记录最后使用时间。失败只打日志不阻断请求——
// 认证已经通过，统计字段缺失不该影响转发。
func (s *Store) TouchLastUsed(ctx context.Context, id int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE vkeys SET last_used_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), id)
	return err
}

// ListVKeys 含 disabled，供 CLI/UI。永不返回明文（本来就没了）。
func (s *Store) ListVKeys(ctx context.Context) ([]VKey, error) {
	rows, err := s.q.QueryContext(ctx, `
		SELECT id, app_name, key_hash, key_prefix, enabled, created_at, COALESCE(last_used_at, 0)
		FROM vkeys ORDER BY app_name`)
	if err != nil {
		return nil, fmt.Errorf("列 vkeys: %w", err)
	}
	defer rows.Close()
	var out []VKey
	for rows.Next() {
		var v VKey
		if err := rows.Scan(&v.ID, &v.AppName, &v.KeyHash, &v.KeyPrefix, &v.Enabled, &v.CreatedAt, &v.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SetVKeyEnabled(ctx context.Context, appName string, enabled bool) error {
	res, err := s.q.ExecContext(ctx, `UPDATE vkeys SET enabled = ? WHERE app_name = ?`, enabled, appName)
	if err != nil {
		return fmt.Errorf("改 vkey %s enabled: %w", appName, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrVKeyNotFound
	}
	return nil
}

func (s *Store) DeleteVKey(ctx context.Context, appName string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM vkeys WHERE app_name = ?`, appName)
	if err != nil {
		return fmt.Errorf("删 vkey %s: %w", appName, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrVKeyNotFound
	}
	return nil
}

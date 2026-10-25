package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrProviderNotFound = errors.New("provider 不存在")

// Provider 是一个上游服务（BYOK）。name 同时是请求路由前缀：
// 客户端 model 写作 "deepseek/deepseek-chat"，前缀命中 providers.name。
type Provider struct {
	ID        int64
	Name      string
	Kind      string // "openai-compat" | "systemone"
	BaseURL   string // 不含尾斜杠
	APIKey    string
	Region    string // "domestic" | "overseas"，M3 级联路由依据
	Enabled   bool
	CreatedAt int64
	UpdatedAt int64
}

// UpsertProvider 按 name 插入或更新（重复 add = 更新连接信息并重新启用）。
// 这是 CLI 的唯一写入口，语义是"我给你的是可用的"。
func (s *Store) UpsertProvider(ctx context.Context, p Provider) (Provider, error) {
	now := time.Now().UnixMilli()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	err := s.q.QueryRowContext(ctx, `
		INSERT INTO providers (name, kind, base_url, api_key, region, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			kind = excluded.kind,
			base_url = excluded.base_url,
			api_key = excluded.api_key,
			region = excluded.region,
			enabled = 1,
			updated_at = excluded.updated_at
		RETURNING id, created_at, updated_at`,
		p.Name, p.Kind, p.BaseURL, p.APIKey, p.Region, p.Enabled, p.CreatedAt, p.UpdatedAt,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Provider{}, fmt.Errorf("upsert provider %s: %w", p.Name, err)
	}
	return p, nil
}

// GetProviderByNameAny 含 disabled 版本，管理端编辑用（"改了就是改了"）。
func (s *Store) GetProviderByNameAny(ctx context.Context, name string) (Provider, error) {
	var p Provider
	err := s.q.QueryRowContext(ctx, `
		SELECT id, name, kind, base_url, api_key, region, enabled, created_at, updated_at
		FROM providers WHERE name = ?`, name,
	).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Region, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("查 provider %s: %w", name, err)
	}
	return p, nil
}

// GetProviderByName 是请求热路径：只返回 enabled 的 provider。
// 不存在与已禁用对外同错（ErrProviderNotFound），避免向探测者泄露登记清单。
func (s *Store) GetProviderByName(ctx context.Context, name string) (Provider, error) {
	var p Provider
	err := s.q.QueryRowContext(ctx, `
		SELECT id, name, kind, base_url, api_key, region, enabled, created_at, updated_at
		FROM providers WHERE name = ? AND enabled = 1`, name,
	).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Region, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("查 provider %s: %w", name, err)
	}
	return p, nil
}

// GetGeminiProvider 返回唯一的 enabled gemini provider。
// Gemini 原生端点 model 在 URL 路径里（/v1beta/models/{model}:generateContent），
// 客户端不写前缀时路由到它——与 systemone 的"专查"逻辑同构。
func (s *Store) GetGeminiProvider(ctx context.Context) (Provider, error) {
	var p Provider
	err := s.q.QueryRowContext(ctx, `
		SELECT id, name, kind, base_url, api_key, region, enabled, created_at, updated_at
		FROM providers WHERE kind = 'gemini' AND enabled = 1
		ORDER BY id LIMIT 1`,
	).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Region, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("查 gemini provider: %w", err)
	}
	return p, nil
}

// GetSystemOneProvider 返回唯一的 enabled systemone provider。
// /v1/systemone 端点不按 model 前缀路由（Jev 是独立协议形态），固定取它。
func (s *Store) GetSystemOneProvider(ctx context.Context) (Provider, error) {
	var p Provider
	err := s.q.QueryRowContext(ctx, `
		SELECT id, name, kind, base_url, api_key, region, enabled, created_at, updated_at
		FROM providers WHERE kind = 'systemone' AND enabled = 1
		ORDER BY id LIMIT 1`,
	).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Region, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("查 systemone provider: %w", err)
	}
	return p, nil
}

// ListProviders 含 disabled，供 CLI/UI 展示全量登记。
func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.q.QueryContext(ctx, `
		SELECT id, name, kind, base_url, api_key, region, enabled, created_at, updated_at
		FROM providers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列 providers: %w", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Region, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SetProviderEnabled(ctx context.Context, name string, enabled bool) error {
	res, err := s.q.ExecContext(ctx, `UPDATE providers SET enabled = ?, updated_at = ? WHERE name = ?`,
		enabled, time.Now().UnixMilli(), name)
	if err != nil {
		return fmt.Errorf("改 provider %s enabled: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (s *Store) DeleteProvider(ctx context.Context, name string) error {
	// 先删 models 引用，避免外键失败；不用级联删除是刻意的——
	// 删 provider 是大动作，模型登记一起消失应该由调用方显式看到。
	if _, err := s.q.ExecContext(ctx, `DELETE FROM models WHERE provider_id = (SELECT id FROM providers WHERE name = ?)`, name); err != nil {
		return fmt.Errorf("删 provider %s 的模型: %w", name, err)
	}
	res, err := s.q.ExecContext(ctx, `DELETE FROM providers WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("删 provider %s: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProviderNotFound
	}
	return nil
}

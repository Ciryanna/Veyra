package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrModelNotFound = errors.New("模型不存在")

// Model 是模型登记册条目：对外名 name（不含前缀）→ 上游名 upstream_model，
// 附带计费价格。注意它是登记册不是白名单：请求未登记的模型仍会透传
// （提供什么用什么），登记只为 /v1/models 列表、改名映射与成本核算。
type Model struct {
	ID            int64
	ProviderID    int64
	ProviderName  string // 查询时 JOIN 出来，写入时忽略
	Name          string
	UpstreamModel string
	PriceInNano   int64 // 每输入 token 的 nanoUSD；0 = 未定价
	PriceOutNano  int64 // 每输出 token 的 nanoUSD；0 = 未定价
	Enabled       bool
	CreatedAt     int64
}

// CostFor 计算一次调用的成本（nanoUSD）。纯整数乘加，无浮点累计误差。
// 价格为 0（未定价）时成本为 0——与 token 缺失同理：宁可 0 让配置问题
// 可见，不做估算。
func (m Model) CostFor(inputTokens, outputTokens int64) int64 {
	return inputTokens*m.PriceInNano + outputTokens*m.PriceOutNano
}

// USDPerMtokToNano 把"美元每百万 token"的人类单位换算为 nanoUSD/token
// 存储单位（×1000）：$3/Mtok → 3000，$0.042/Mtok → 42。
// 负数与 NaN 原样截断为 0（拒绝异常输入进库）。
func USDPerMtokToNano(usd float64) int64 {
	if usd <= 0 || usd != usd { // 负价与 NaN 无意义
		return 0
	}
	return int64(usd*1000 + 0.5)
}

// NanoToUSD 把 nanoUSD 换算回美元（展示用）。
func NanoToUSD(n int64) float64 {
	return float64(n) / 1e9
}

// ModelInput 是 UpsertModel 的写入参数。
// 价格约定：非零即更新，0 = 保留原值（无法"清零"是刻意的——
// 要改价就给新值，"未指定不动旧值"是补价格场景的正确默认）。
type ModelInput struct {
	Provider      string
	Name          string
	UpstreamModel string // 空则等于 Name
	PriceInNano   int64
	PriceOutNano  int64
}

// UpsertModel 按 (provider, name) 插入或更新映射与价格。
func (s *Store) UpsertModel(ctx context.Context, in ModelInput) (Model, error) {
	if in.UpstreamModel == "" {
		in.UpstreamModel = in.Name
	}
	var m Model
	err := s.q.QueryRowContext(ctx, `
		INSERT INTO models (provider_id, name, upstream_model, price_in_nano, price_out_nano, enabled, created_at)
		VALUES ((SELECT id FROM providers WHERE name = ?), ?, ?, ?, ?, 1, ?)
		ON CONFLICT(provider_id, name) DO UPDATE SET
			upstream_model = excluded.upstream_model,
			price_in_nano = CASE WHEN excluded.price_in_nano > 0
				THEN excluded.price_in_nano ELSE models.price_in_nano END,
			price_out_nano = CASE WHEN excluded.price_out_nano > 0
				THEN excluded.price_out_nano ELSE models.price_out_nano END,
			enabled = 1
		RETURNING id, provider_id, name, upstream_model, price_in_nano, price_out_nano, created_at`,
		in.Provider, in.Name, in.UpstreamModel, in.PriceInNano, in.PriceOutNano, time.Now().UnixMilli(),
	).Scan(&m.ID, &m.ProviderID, &m.Name, &m.UpstreamModel, &m.PriceInNano, &m.PriceOutNano, &m.CreatedAt)
	if err != nil {
		return Model{}, fmt.Errorf("upsert model %s/%s: %w", in.Provider, in.Name, err)
	}
	m.ProviderName, m.Enabled = in.Provider, true
	return m, nil
}

// GetModel 路由热路径：enabled 的 provider + enabled 的模型才命中。
// 价格一并返回（记账路径用同一次查询，不额外加往返）。
func (s *Store) GetModel(ctx context.Context, providerName, name string) (Model, error) {
	var m Model
	err := s.q.QueryRowContext(ctx, `
		SELECT m.id, m.provider_id, p.name, m.name, m.upstream_model,
			m.price_in_nano, m.price_out_nano, m.enabled
		FROM models m JOIN providers p ON p.id = m.provider_id
		WHERE p.name = ? AND p.enabled = 1 AND m.name = ? AND m.enabled = 1`,
		providerName, name,
	).Scan(&m.ID, &m.ProviderID, &m.ProviderName, &m.Name, &m.UpstreamModel,
		&m.PriceInNano, &m.PriceOutNano, &m.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrModelNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("查 model %s/%s: %w", providerName, name, err)
	}
	return m, nil
}

// ListModels 供 /v1/models 与 CLI：仅 enabled provider 的 enabled 模型。
// 对外 id 是 "provider/model"，与请求里的写法一致。
func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.q.QueryContext(ctx, `
		SELECT m.id, m.provider_id, p.name, m.name, m.upstream_model,
			m.price_in_nano, m.price_out_nano, m.enabled
		FROM models m JOIN providers p ON p.id = m.provider_id
		WHERE p.enabled = 1 AND m.enabled = 1
		ORDER BY p.name, m.name`)
	if err != nil {
		return nil, fmt.Errorf("列 models: %w", err)
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &m.Name, &m.UpstreamModel,
			&m.PriceInNano, &m.PriceOutNano, &m.Enabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SetModelEnabled(ctx context.Context, providerName, name string, enabled bool) error {
	res, err := s.q.ExecContext(ctx, `
		UPDATE models SET enabled = ? WHERE name = ?
		AND provider_id = (SELECT id FROM providers WHERE name = ?)`, enabled, name, providerName)
	if err != nil {
		return fmt.Errorf("改 model %s/%s enabled: %w", providerName, name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrModelNotFound
	}
	return nil
}

func (s *Store) DeleteModel(ctx context.Context, providerName, name string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM models WHERE name = ?
		AND provider_id = (SELECT id FROM providers WHERE name = ?)`, name, providerName)
	if err != nil {
		return fmt.Errorf("删 model %s/%s: %w", providerName, name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrModelNotFound
	}
	return nil
}

package store

import (
	"context"
	"fmt"
	"time"
)

// UsageEvent 是一次上游调用的记账行。无论成败都落一行：
// 失败行（status_code=0 / 4xx / 5xx）同样是"这个应用在干什么"的观测数据。
type UsageEvent struct {
	ID           int64
	VKeyID       int64
	ProviderID   int64 // 0 = 未路由到 provider（网络失败/配置缺失）
	Model        string
	Endpoint     string // "chat" | "systemone"
	InputTokens  int64
	OutputTokens int64
	StatusCode   int
	DurationMs   int64
	// CostNano 是记账时锁定的成本（nanoUSD，1e-9 USD）：价格取自当时的
	// models 表，之后改价不改历史——"当时花的钱"才是账实相符。
	CostNano  int64
	ErrorMsg  string
	CreatedAt int64

	// AppName 是运行时携带的展示字段（httpapi 业务日志用），不落库——
	// vkeys 表才是应用名的真相，记账行只存 vkey_id 外键。
	AppName string `json:"-"`

	// PriceInNano/PriceOutNano 是运行时携带的计费单价（路由时从 models
	// 表带出），不落库。recordUsage 据此把 CostNano 锁进记账行：
	// 之后改价不改历史。级联场景下价格配置在哪一跳，成本就记在哪一跳
	// （egress 中转不重复计费）。
	PriceInNano  int64 `json:"-"`
	PriceOutNano int64 `json:"-"`
}

func (s *Store) InsertUsageEvent(ctx context.Context, e UsageEvent) error {
	e.CreatedAt = time.Now().UnixMilli()
	// ProviderID=0 表示未路由到任何 provider（网络失败/配置缺失），落 NULL：
	// 外键约束下 0 会炸，且 NULL 才是"没有"的正确表达。
	var providerID any
	if e.ProviderID > 0 {
		providerID = e.ProviderID
	}
	res, err := s.q.ExecContext(ctx, `
		INSERT INTO usage_events (vkey_id, provider_id, model, endpoint,
			input_tokens, output_tokens, status_code, duration_ms, cost_nano, error_msg, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.VKeyID, providerID, e.Model, e.Endpoint,
		e.InputTokens, e.OutputTokens, e.StatusCode, e.DurationMs, e.CostNano, e.ErrorMsg, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("落 usage_events: %w", err)
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// UsageRow 是聚合结果。请求数与 token 之和按维度分组。
type UsageRow struct {
	Label         string // 应用名或 provider 名
	Requests      int64
	InputTokens   int64
	OutputTokens  int64
	TotalTokens   int64
	ErrorCount    int64 // status_code 为 0 或 >=400
	AvgDurationMs int64
	CostNano      int64
}

// UsageByVKey 按应用聚合，since/until 为 Unix 毫秒（0 = 不限）。
func (s *Store) UsageByVKey(ctx context.Context, since, until int64) ([]UsageRow, error) {
	return usageAggregate(ctx, s.q, `
		SELECT v.app_name AS label, COUNT(*) AS requests,
			COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0),
			COALESCE(SUM(CASE WHEN e.status_code = 0 OR e.status_code >= 400 THEN 1 ELSE 0 END), 0),
			CAST(COALESCE(AVG(e.duration_ms), 0) AS INTEGER),
			COALESCE(SUM(e.cost_nano), 0)
		FROM usage_events e JOIN vkeys v ON v.id = e.vkey_id`, since, until,
		"GROUP BY v.app_name ORDER BY requests DESC")
}

// UsageByProvider 按上游聚合，since/until 为 Unix 毫秒（0 = 不限）。
func (s *Store) UsageByProvider(ctx context.Context, since, until int64) ([]UsageRow, error) {
	return usageAggregate(ctx, s.q, `
		SELECT COALESCE(p.name, '(未路由)') AS label, COUNT(*) AS requests,
			COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0),
			COALESCE(SUM(CASE WHEN e.status_code = 0 OR e.status_code >= 400 THEN 1 ELSE 0 END), 0),
			CAST(COALESCE(AVG(e.duration_ms), 0) AS INTEGER),
			COALESCE(SUM(e.cost_nano), 0)
		FROM usage_events e LEFT JOIN providers p ON p.id = e.provider_id`, since, until,
		"GROUP BY label ORDER BY requests DESC")
}

func usageAggregate(ctx context.Context, q querier, selectHead string, since, until int64, tail string) ([]UsageRow, error) {
	where, args := "", []any{}
	if since > 0 {
		where += " WHERE e.created_at >= ?"
		args = append(args, since)
	}
	if until > 0 {
		if where == "" {
			where += " WHERE"
		} else {
			where += " AND"
		}
		where += " e.created_at < ?"
		args = append(args, until)
	}
	rows, err := q.QueryContext(ctx, selectHead+where+" "+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("聚合 usage: %w", err)
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Label, &r.Requests, &r.InputTokens, &r.OutputTokens, &r.ErrorCount, &r.AvgDurationMs, &r.CostNano); err != nil {
			return nil, err
		}
		r.TotalTokens = r.InputTokens + r.OutputTokens
		out = append(out, r)
	}
	return out, rows.Err()
}

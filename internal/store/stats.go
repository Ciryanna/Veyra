package store

import (
	"context"
	"fmt"
)

// Stats 是仪表盘概览的聚合块（M2 UI 首屏卡片）。
type Stats struct {
	Requests   int64 // 窗口内总请求数
	InputTok   int64
	OutputTok  int64
	Errors     int64 // status_code = 0（网络）或 >= 400
	ActiveApps int64 // 窗口内有调用的应用数
	CostNano   int64 // 窗口内累计成本（未定价模型贡献 0）
}

// StatsSince 聚合 since（Unix 毫秒）之后的用量。since=0 即全量。
func (s *Store) StatsSince(ctx context.Context, since int64) (Stats, error) {
	var st Stats
	err := s.q.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN status_code = 0 OR status_code >= 400 THEN 1 ELSE 0 END), 0),
			COUNT(DISTINCT vkey_id),
			COALESCE(SUM(cost_nano), 0)
		FROM usage_events WHERE created_at >= ?`, since,
	).Scan(&st.Requests, &st.InputTok, &st.OutputTok, &st.Errors, &st.ActiveApps, &st.CostNano)
	if err != nil {
		return Stats{}, fmt.Errorf("聚合 stats: %w", err)
	}
	return st, nil
}

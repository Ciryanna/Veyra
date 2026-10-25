package store_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/Ciryanna/Veyra/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	return st
}

func TestMigrateIdempotent(t *testing.T) {
	st := openTestStore(t)
	// 第二次 Migrate 必须无副作用——重复执行是升级部署的常态。
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
}

func TestVKeyLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	key1, _, err := st.CreateVKey(ctx, "qa-bot")
	if err != nil {
		t.Fatalf("签发: %v", err)
	}
	if len(key1) != 51 { // vk- + 48 hex
		t.Fatalf("key 长度异常: %d", len(key1))
	}
	if _, err := st.FindVKeyByHash(ctx, store.HashVKey(key1)); err != nil {
		t.Fatalf("签发后应可认证: %v", err)
	}

	// 同名重签 = 重置凭证：旧 key 立即失效。
	key2, _, err := st.CreateVKey(ctx, "qa-bot")
	if err != nil {
		t.Fatalf("重签: %v", err)
	}
	if key1 == key2 {
		t.Fatal("重签应产生新 key")
	}
	if _, err := st.FindVKeyByHash(ctx, store.HashVKey(key1)); !errors.Is(err, store.ErrVKeyNotFound) {
		t.Fatalf("旧 key 应失效，得到 %v", err)
	}
	if _, err := st.FindVKeyByHash(ctx, store.HashVKey(key2)); err != nil {
		t.Fatalf("新 key 应可用: %v", err)
	}

	// 禁用后认证失败，与不存在同错。
	if err := st.SetVKeyEnabled(ctx, "qa-bot", false); err != nil {
		t.Fatalf("禁用: %v", err)
	}
	if _, err := st.FindVKeyByHash(ctx, store.HashVKey(key2)); !errors.Is(err, store.ErrVKeyNotFound) {
		t.Fatalf("禁用后应拒绝，得到 %v", err)
	}
}

func TestProviderRouting(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if _, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记: %v", err)
	}
	p, err := st.GetProviderByName(ctx, "deepseek")
	if err != nil || p.APIKey != "sk-test" {
		t.Fatalf("路由查询失败: %v %+v", err, p)
	}

	// upsert 更新连接信息。
	if _, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-new", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("更新: %v", err)
	}
	if p, _ := st.GetProviderByName(ctx, "deepseek"); p.APIKey != "sk-new" {
		t.Fatalf("upsert 应更新 key，得到 %q", p.APIKey)
	}

	if err := st.SetProviderEnabled(ctx, "deepseek", false); err != nil {
		t.Fatalf("禁用: %v", err)
	}
	if _, err := st.GetProviderByName(ctx, "deepseek"); !errors.Is(err, store.ErrProviderNotFound) {
		t.Fatalf("禁用后应不可路由，得到 %v", err)
	}

	// systemone 专查：只认 kind，不认名字。
	if _, err := st.UpsertProvider(ctx, store.Provider{
		Name: "typesafe", Kind: "systemone",
		BaseURL: "https://api.typesafe.ai/v1", APIKey: "ts-test", Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 systemone: %v", err)
	}
	sp, err := st.GetSystemOneProvider(ctx)
	if err != nil || sp.Name != "typesafe" {
		t.Fatalf("systemone 查询失败: %v %+v", err, sp)
	}
}

func TestModelMapping(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://x/v1", APIKey: "k", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 provider: %v", err)
	}
	if _, err := st.UpsertModel(ctx, store.ModelInput{Provider: "deepseek", Name: "chat", UpstreamModel: "deepseek-chat-v3"}); err != nil {
		t.Fatalf("登记模型: %v", err)
	}
	m, err := st.GetModel(ctx, "deepseek", "chat")
	if err != nil || m.UpstreamModel != "deepseek-chat-v3" {
		t.Fatalf("映射查询失败: %v %+v", err, m)
	}
	if _, err := st.GetModel(ctx, "deepseek", "unregistered"); !errors.Is(err, store.ErrModelNotFound) {
		t.Fatalf("未登记应报错（调用方决定是否透传），得到 %v", err)
	}

	ms, err := st.ListModels(ctx)
	if err != nil || len(ms) != 1 || ms[0].ProviderName != "deepseek" {
		t.Fatalf("模型列表异常: %v %+v", err, ms)
	}
}

func TestUsageAggregate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	p, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://x/v1", APIKey: "k", Region: "domestic", Enabled: true,
	})
	if err != nil {
		t.Fatalf("登记 provider: %v", err)
	}
	_, _, err = st.CreateVKey(ctx, "app-a")
	if err != nil {
		t.Fatalf("签发: %v", err)
	}
	_, v2, err := st.CreateVKey(ctx, "app-b")
	if err != nil {
		t.Fatalf("签发: %v", err)
	}
	events := []store.UsageEvent{
		{VKeyID: v2.ID, ProviderID: p.ID, Model: "deepseek/chat", Endpoint: "chat",
			InputTokens: 100, OutputTokens: 50, StatusCode: 200, DurationMs: 800},
		{VKeyID: v2.ID, ProviderID: p.ID, Model: "deepseek/chat", Endpoint: "chat",
			InputTokens: 30, OutputTokens: 0, StatusCode: 429, DurationMs: 200},
		{VKeyID: v2.ID, ProviderID: 0, Model: "openai/gpt-x", Endpoint: "chat",
			StatusCode: 0, DurationMs: 100, ErrorMsg: "dial tcp: timeout"},
	}
	for _, e := range events {
		if err := st.InsertUsageEvent(ctx, e); err != nil {
			t.Fatalf("落账: %v", err)
		}
	}
	rows, err := st.UsageByVKey(ctx, 0, 0)
	if err != nil {
		t.Fatalf("聚合: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应有 1 个应用有记录，得到 %d", len(rows))
	}
	r := rows[0]
	if r.Label != "app-b" || r.Requests != 3 || r.InputTokens != 130 || r.OutputTokens != 50 {
		t.Fatalf("聚合数字错误: %+v", r)
	}
	if r.ErrorCount != 2 { // 429 + 网络错误
		t.Fatalf("错误计数应为 2: %+v", r)
	}
	if r.AvgDurationMs != 366 { // (800+200+100)/3 = 366.67 → SQLite AVG 取整 366
		t.Fatalf("平均耗时不符: %d", r.AvgDurationMs)
	}
}

func TestModelPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://x/v1", APIKey: "k", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 provider: %v", err)
	}

	// 换算：$0.27/Mtok → 270 nanoUSD/token；$1.1/Mtok → 1100。
	if n := store.USDPerMtokToNano(0.27); n != 270 {
		t.Fatalf("price-in 换算错误: %d", n)
	}
	if n := store.USDPerMtokToNano(1.1); n != 1100 {
		t.Fatalf("price-out 换算错误: %d", n)
	}
	// 异常输入归零（负价/NaN 无意义）。
	if n := store.USDPerMtokToNano(-1); n != 0 {
		t.Fatalf("负价应归零: %d", n)
	}
	if n := store.USDPerMtokToNano(math.NaN()); n != 0 {
		t.Fatalf("NaN 应归零: %d", n)
	}

	m, err := st.UpsertModel(ctx, store.ModelInput{
		Provider: "deepseek", Name: "chat",
		PriceInNano:  store.USDPerMtokToNano(0.27),
		PriceOutNano: store.USDPerMtokToNano(1.1),
	})
	if err != nil {
		t.Fatalf("登记带价模型: %v", err)
	}
	// 成本纯整数乘加：1000 in + 2000 out = 270k + 2200k nanoUSD = $0.00247。
	if c := m.CostFor(1000, 2000); c != 1000*270+2000*1100 {
		t.Fatalf("CostFor 错误: %d", c)
	}
	if usd := store.NanoToUSD(m.CostFor(1000, 2000)); usd < 0.002469 || usd > 0.002471 {
		t.Fatalf("成本换算错误: %g", usd)
	}

	// 非零即更新：只传 price-in 时 price-out 保留。
	m2, err := st.UpsertModel(ctx, store.ModelInput{
		Provider: "deepseek", Name: "chat",
		PriceInNano: store.USDPerMtokToNano(0.14),
	})
	if err != nil {
		t.Fatalf("补价: %v", err)
	}
	if m2.PriceInNano != 140 || m2.PriceOutNano != 1100 {
		t.Fatalf("未指定的价格应保留: %+v", m2)
	}

	// GetModel 路径一并返回价格。
	got, err := st.GetModel(ctx, "deepseek", "chat")
	if err != nil || got.PriceInNano != 140 || got.PriceOutNano != 1100 {
		t.Fatalf("GetModel 价格错误: %v %+v", err, got)
	}
}

func TestUsageCostAggregate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	p, err := st.UpsertProvider(ctx, store.Provider{
		Name: "deepseek", Kind: "openai-compat",
		BaseURL: "https://x/v1", APIKey: "k", Region: "domestic", Enabled: true,
	})
	if err != nil {
		t.Fatalf("登记 provider: %v", err)
	}
	if _, err := st.UpsertModel(ctx, store.ModelInput{
		Provider: "deepseek", Name: "chat",
		PriceInNano: 270, PriceOutNano: 1100,
	}); err != nil {
		t.Fatalf("登记价格: %v", err)
	}
	_, v, err := st.CreateVKey(ctx, "app-cost")
	if err != nil {
		t.Fatalf("签发: %v", err)
	}
	// 成本记账由 httpapi 的 recordUsage 统一计算，store 只存；这里模拟
	// recordUsage 的算式（与实现同式，双保险锁定单位约定）。
	e := store.UsageEvent{
		VKeyID: v.ID, ProviderID: p.ID, Model: "deepseek/chat", Endpoint: "chat",
		InputTokens: 1000, OutputTokens: 2000, StatusCode: 200,
		PriceInNano: 270, PriceOutNano: 1100,
	}
	e.CostNano = e.InputTokens*e.PriceInNano + e.OutputTokens*e.PriceOutNano
	if err := st.InsertUsageEvent(ctx, e); err != nil {
		t.Fatalf("落账: %v", err)
	}
	rows, err := st.UsageByVKey(ctx, 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("聚合: %v %+v", err, rows)
	}
	// 270k + 2200k = 2470k nanoUSD = $0.00247。
	if rows[0].CostNano != 1000*270+2000*1100 {
		t.Fatalf("聚合成本错误: %d", rows[0].CostNano)
	}
	stats, err := st.StatsSince(ctx, 0)
	if err != nil || stats.CostNano != rows[0].CostNano {
		t.Fatalf("stats 成本错误: %v %+v", err, stats)
	}
}

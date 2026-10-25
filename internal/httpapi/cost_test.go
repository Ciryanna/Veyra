package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/store"
)

func TestChatCostAccounting(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	// 定价：$0.27/Mtok 输入、$1.10/Mtok 输出（fake-chat 上游名映射 fake-chat-v3
	// 顺带验证带价 upsert 保留映射）。
	if _, err := g.st.UpsertModel(t.Context(), store.ModelInput{
		Provider: "fake", Name: "fake-chat", UpstreamModel: "fake-chat-v3",
		PriceInNano: store.USDPerMtokToNano(0.27), PriceOutNano: store.USDPerMtokToNano(1.10),
	}); err != nil {
		t.Fatalf("登记带价模型: %v", err)
	}
	key := g.newVKey("cost-app")

	// 非流式：5 in + 3 out → 5*270 + 3*1100 = 4650 nanoUSD。
	resp, out := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("转发失败: %d %s", resp.StatusCode, out)
	}
	if !strings.Contains(g.lastUpReq().Body, `"model":"fake-chat-v3"`) {
		t.Fatalf("上游映射应仍生效: %s", g.lastUpReq().Body)
	}
	rows := g.usageRows()
	if len(rows) != 1 || rows[0].CostNano != 5*270+3*1100 {
		t.Fatalf("非流式成本错误: %+v", rows)
	}
}

func TestChatCostStreamAccounting(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	if _, err := g.st.UpsertModel(t.Context(), store.ModelInput{
		Provider: "fake", Name: "fake-chat",
		PriceInNano: store.USDPerMtokToNano(0.27), PriceOutNano: store.USDPerMtokToNano(1.10),
	}); err != nil {
		t.Fatalf("登记带价模型: %v", err)
	}
	key := g.newVKey("cost-s")

	// 流式 usage chunk：7 in + 2 out → 7*270 + 2*1100 = 4090 nanoUSD。
	resp, out := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("流式失败: %d", resp.StatusCode)
	}
	rows := g.usageRows()
	if rows[0].CostNano != 7*270+2*1100 {
		t.Fatalf("流式成本错误: %+v", rows)
	}
}

func TestUnpricedModelCostZero(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	key := g.newVKey("cost-zero")

	// 未登记价格 → 成本 0（不估算约定）。
	resp, _ := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatal("转发失败")
	}
	rows := g.usageRows()
	if rows[0].CostNano != 0 {
		t.Fatalf("未定价成本应为 0: %+v", rows)
	}
}

func TestSystemOneCostAccounting(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-latest","answers":{},"usage":{"input_tokens":120,"output_tokens":12}}`)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	if _, err := g.st.UpsertProvider(t.Context(), store.Provider{
		Name: "typesafe", Kind: "systemone",
		BaseURL: g.upstream.URL + "/v1", APIKey: "ts-key", Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 systemone: %v", err)
	}
	// Jev 只收输入费：$0.042/Mtok → 42 nanoUSD/token。
	if _, err := g.st.UpsertModel(t.Context(), store.ModelInput{
		Provider: "typesafe", Name: "jev-latest",
		PriceInNano: store.USDPerMtokToNano(0.042),
	}); err != nil {
		t.Fatalf("登记 jev 价格: %v", err)
	}
	key := g.newVKey("cost-jev")

	resp, out := g.do("POST", "/v1/systemone", key,
		[]byte(`{"model":"typesafe/jev-latest","state":"hi","questions":{"q":{"type":"noul","instructions":"?"}}}`))
	if resp.StatusCode != 200 {
		t.Fatalf("systemone 失败: %d %s", resp.StatusCode, out)
	}
	rows := g.usageRows()
	// 120 in * 42 + 12 out * 0（输出免费）= 5040 nanoUSD。
	if rows[0].CostNano != 120*42 {
		t.Fatalf("systemone 成本错误（输出应为免费）: %+v", rows)
	}
}

package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/httpapi"
	"github.com/Ciryanna/Veyra/internal/store"
)

// newEgressTopology 搭双节点拓扑：
//
//	应用 → primary（region=overseas 改道）→ egress（节点 vkey 认证）→ 假上游
//
// primary 上 overseas provider 只登记占位（base_url/api_key 假值），
// 真实凭证（fake → 假上游）在 egress 侧——这正是部署形态。
func newEgressTopology(t *testing.T) (primary, egress *gateway, nodeKey string) {
	t.Helper()

	egress = newTestGateway(t) // 含假上游 provider "fake"（sk-upstream）
	nodeKey = egress.newVKey("primary-node")

	// primary：独立 store，overseas provider 仅占位。
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "primary.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	if _, err := st.UpsertProvider(t.Context(), store.Provider{
		Name: "fake", Kind: "openai-compat",
		BaseURL: "http://placeholder.invalid/v1", APIKey: "placeholder",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("登记占位 provider: %v", err)
	}
	// 一个 domestic provider 验证"国内直连不受改道影响"。
	if _, err := st.UpsertProvider(t.Context(), store.Provider{
		Name: "local", Kind: "openai-compat",
		BaseURL: egress.upstream.URL + "/v1", APIKey: "sk-upstream",
		Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 local provider: %v", err)
	}

	srv := httptest.NewServer(httpapi.New(httpapi.Options{
		Store:     st,
		Log:       discardLogger(),
		EgressURL: egress.url,
		EgressKey: nodeKey,
	}).Handler())
	t.Cleanup(srv.Close)

	primary = &gateway{t: t, st: st, url: srv.URL, upstream: egress.upstream}
	return primary, egress, nodeKey
}

func TestEgressRoutingChat(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = defaultBehavior
	key := primary.newVKey("app-x")

	resp, out := primary.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"prompt_tokens":5`) {
		t.Fatalf("二跳转发失败: %d %s", resp.StatusCode, out)
	}

	// 假上游最终收到的：剥掉 fake/ 前缀的真实模型名 + 真实上游 key。
	up := egress.lastUpReq()
	if !strings.Contains(up.Body, `"model":"fake-chat"`) {
		t.Fatalf("egress 转给上游的 model 应为 fake-chat: %s", up.Body)
	}
	if up.Auth != "Bearer sk-upstream" {
		t.Fatalf("上游应收到 egress 侧真实 key: %s", up.Auth)
	}

	// egress 节点用自己的节点 vkey 认了这次内部调用并记账。
	rows, err := egress.st.UsageByVKey(t.Context(), 0, 0)
	if err != nil || len(rows) != 1 || rows[0].Label != "primary-node" {
		t.Fatalf("egress 侧应按节点 vkey 记账: %v %+v", err, rows)
	}
	// primary 侧也记了账（应用维度）。
	pRows, err := primary.st.UsageByVKey(t.Context(), 0, 0)
	if err != nil || len(pRows) != 1 || pRows[0].Label != "app-x" {
		t.Fatalf("primary 侧应按应用记账: %v %+v", err, pRows)
	}
}

func TestEgressRoutingStream(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = defaultBehavior
	key := primary.newVKey("app-stream")

	resp, out := primary.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("流式二跳失败: %d %s", resp.StatusCode, out)
	}
	// usage 在两跳各自抓取。
	pRows, _ := primary.st.UsageByVKey(t.Context(), 0, 0)
	if pRows[0].InputTokens != 7 || pRows[0].OutputTokens != 2 {
		t.Fatalf("primary 流式 usage 错误: %+v", pRows)
	}
	eRows, _ := egress.st.UsageByVKey(t.Context(), 0, 0)
	if eRows[0].InputTokens != 7 {
		t.Fatalf("egress 流式 usage 错误: %+v", eRows)
	}
}

func TestEgressRoutingDomesticDirect(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = defaultBehavior
	key := primary.newVKey("app-d")

	// domestic provider 不改道：直接打到 base_url 指向的地址（同一假上游）。
	// 注意直连快照记在 egress 的假上游实例里（primary 复用其 upstream）。
	resp, out := primary.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"local/any-model","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("domestic 直连失败: %d %s", resp.StatusCode, out)
	}
	if !strings.Contains(egress.lastUpReq().Body, `"model":"any-model"`) {
		t.Fatalf("直连 model 应剥前缀: %s", egress.lastUpReq().Body)
	}
}

func TestEgressRoutingSystemOne(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-latest","answers":{},"usage":{"input_tokens":42,"output_tokens":4}}`)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	// egress 侧配置 systemone provider；primary 侧只放占位。
	if _, err := egress.st.UpsertProvider(t.Context(), store.Provider{
		Name: "typesafe", Kind: "systemone",
		BaseURL: egress.upstream.URL + "/v1", APIKey: "ts-key", Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("egress 登记 systemone: %v", err)
	}
	if _, err := primary.st.UpsertProvider(t.Context(), store.Provider{
		Name: "typesafe", Kind: "systemone",
		BaseURL: "http://placeholder.invalid/v1", APIKey: "placeholder",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("primary 登记占位 systemone: %v", err)
	}

	key := primary.newVKey("app-jev")
	resp, out := primary.do("POST", "/v1/systemone", key,
		[]byte(`{"model":"typesafe/jev-latest","state":"hi","questions":{"q":{"type":"noul","instructions":"?"}}}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"input_tokens":42`) {
		t.Fatalf("systemone 二跳失败: %d %s", resp.StatusCode, out)
	}
	// 假上游收到 egress 的真实 key。
	if egress.lastUpReq().Auth != "Bearer ts-key" {
		t.Fatalf("上游应收到 ts-key: %s", egress.lastUpReq().Auth)
	}
}

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/httpapi"
	"github.com/Ciryanna/Veyra/internal/store"
)

// newAdminGateway 在共用同一 store 的前提下，另起一个带管理令牌的网关实例。
func newAdminGateway(t *testing.T) *gateway {
	t.Helper()
	g := newTestGateway(t)
	srv := httptest.NewServer(httpapi.New(httpapi.Options{
		Store: g.st, Log: discardLogger(), AdminToken: "admin-tok",
	}).Handler())
	t.Cleanup(srv.Close)
	g.url = srv.URL
	return g
}

// adminDo 打管理端请求（Bearer admin-tok）。
func (g *gateway) adminDo(method, path string, body any) (*http.Response, string) {
	g.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			g.t.Fatalf("序列化: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, g.url+path, rdr)
	if err != nil {
		g.t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer admin-tok")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatalf("请求: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(out)
}

func TestAdminAuth(t *testing.T) {
	g := newAdminGateway(t)
	resp, out := g.adminDo("GET", "/admin/api/stats", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("正确令牌应 200: %d %s", resp.StatusCode, out)
	}
	// 错误令牌与缺失令牌都 401。
	for _, h := range []string{"Bearer wrong", ""} {
		req, _ := http.NewRequest("GET", g.url+"/admin/api/stats", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求: %v", err)
		}
		if resp.StatusCode != 401 {
			t.Fatalf("令牌 %q 期望 401，得到 %d", h, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestAdminProviderFlow(t *testing.T) {
	g := newAdminGateway(t)

	// 新增。
	resp, out := g.adminDo("PUT", "/admin/api/providers", map[string]any{
		"name": "kimi", "kind": "openai-compat",
		"base_url": "https://api.moonshot.cn/v1", "api_key": "sk-moon-1234567890abcd", "region": "domestic",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("新增 provider 失败: %d %s", resp.StatusCode, out)
	}

	resp, out = g.adminDo("GET", "/admin/api/providers", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("列表失败: %d %s", resp.StatusCode, out)
	}
	var ps []map[string]any
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("非法 JSON: %s", out)
	}
	var kimi map[string]any
	for _, p := range ps {
		if p["name"] == "kimi" {
			kimi = p
		}
	}
	if kimi == nil {
		t.Fatalf("列表应含 kimi: %s", out)
	}
	if masked, _ := kimi["api_key_masked"].(string); masked != "sk-moo…abcd" {
		t.Fatalf("key 应脱敏为 sk-moo…abcd: %q", masked)
	}

	// 编辑不带 key：保留原 key（库里能路由成功即证明）。
	resp, out = g.adminDo("PUT", "/admin/api/providers", map[string]any{
		"name": "kimi", "kind": "openai-compat",
		"base_url": "https://api.moonshot.cn/v1", "region": "domestic",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("编辑失败: %d %s", resp.StatusCode, out)
	}
	p, err := g.st.GetProviderByNameAny(g.t.Context(), "kimi")
	if err != nil || p.APIKey != "sk-moon-1234567890abcd" {
		t.Fatalf("编辑不带 key 应保留原 key: %v %+v", err, p)
	}

	// 禁用 → 路由热路径不可见。
	resp, _ = g.adminDo("PATCH", "/admin/api/providers/enabled", map[string]any{"name": "kimi", "enabled": false})
	if resp.StatusCode != 200 {
		t.Fatal("禁用失败")
	}
	if _, err := g.st.GetProviderByName(g.t.Context(), "kimi"); err == nil {
		t.Fatal("禁用后路由查询应失败")
	}

	// 删除。
	resp, _ = g.adminDo("DELETE", "/admin/api/providers?name=kimi", nil)
	if resp.StatusCode != 200 {
		t.Fatal("删除失败")
	}
	if _, err := g.st.GetProviderByNameAny(g.t.Context(), "kimi"); err == nil {
		t.Fatal("删除后应不存在")
	}
}

func TestAdminVKeyFlow(t *testing.T) {
	g := newAdminGateway(t)

	resp, out := g.adminDo("POST", "/admin/api/vkeys", map[string]any{"app_name": "writer"})
	if resp.StatusCode != 201 {
		t.Fatalf("签发失败: %d %s", resp.StatusCode, out)
	}
	var created struct {
		AppName string `json:"app_name"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || len(created.Key) != 51 {
		t.Fatalf("签发响应应含一次性明文 key: %s", out)
	}
	// 明文 key 可直接走业务认证。
	if _, err := g.st.FindVKeyByHash(g.t.Context(), store.HashVKey(created.Key)); err != nil {
		t.Fatalf("新 key 应可认证: %v", err)
	}

	resp, out = g.adminDo("GET", "/admin/api/vkeys", nil)
	if !strings.Contains(out, `"app_name":"writer"`) {
		t.Fatalf("列表应含 writer: %s", out)
	}
	// 列表绝不回明文或哈希。
	if strings.Contains(out, created.Key) {
		t.Fatalf("列表泄漏明文 key: %s", out)
	}

	resp, _ = g.adminDo("PATCH", "/admin/api/vkeys/enabled", map[string]any{"app_name": "writer", "enabled": false})
	if resp.StatusCode != 200 {
		t.Fatal("禁用失败")
	}
	if _, err := g.st.FindVKeyByHash(g.t.Context(), store.HashVKey(created.Key)); err == nil {
		t.Fatal("禁用后应不可认证")
	}

	resp, _ = g.adminDo("DELETE", "/admin/api/vkeys?app=writer", nil)
	if resp.StatusCode != 200 {
		t.Fatal("删除失败")
	}
}

func TestAdminModelFlow(t *testing.T) {
	g := newAdminGateway(t)
	// 模型名含斜杠（百炼式 id），必须经 body/query 而非路径参数。
	resp, out := g.adminDo("PUT", "/admin/api/models", map[string]any{
		"provider": "fake", "name": "qwen/qwen3-max", "upstream_model": "qwen3-max",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("登记失败: %d %s", resp.StatusCode, out)
	}
	resp, out = g.adminDo("GET", "/admin/api/models", nil)
	if !strings.Contains(out, `"provider":"fake"`) || !strings.Contains(out, `"name":"qwen/qwen3-max"`) {
		t.Fatalf("模型列表异常: %s", out)
	}
	resp, _ = g.adminDo("DELETE", "/admin/api/models?provider=fake&name=qwen/qwen3-max", nil)
	if resp.StatusCode != 200 {
		t.Fatal("删除含斜杠模型名失败")
	}
}

func TestAdminStatsAndUsage(t *testing.T) {
	g := newAdminGateway(t)
	key := g.newVKey("stats-app")
	// 走一次真实转发产生记账。
	upstreamBehavior = defaultBehavior
	resp, _ := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/m","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatal("转发失败")
	}

	resp, out := g.adminDo("GET", "/admin/api/stats?since=1h", nil)
	if resp.StatusCode != 200 || !strings.Contains(out, `"requests":1`) {
		t.Fatalf("stats 异常: %d %s", resp.StatusCode, out)
	}
	resp, out = g.adminDo("GET", "/admin/api/usage?by=vkey", nil)
	if !strings.Contains(out, `"label":"stats-app"`) {
		t.Fatalf("usage 按应用聚合异常: %s", out)
	}
	resp, out = g.adminDo("GET", "/admin/api/usage?by=provider", nil)
	if !strings.Contains(out, `"label":"fake"`) {
		t.Fatalf("usage 按上游聚合异常: %s", out)
	}
}

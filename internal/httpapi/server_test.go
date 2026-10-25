package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ciryanna/Veyra/internal/httpapi"
	"github.com/Ciryanna/Veyra/internal/store"
)

// newTestGateway 起一套完整网关 + 一个可编程假上游。
// 返回网关地址、假上游收到的请求快照列表、落库辅助。
type gateway struct {
	t        *testing.T
	url      string
	upstream *httptest.Server
	st       *store.Store
	mu       sync.Mutex
	upReqs   []upReqSnapshot // 假上游收到的每个请求
}

type upReqSnapshot struct {
	Path string
	Auth string // Authorization 头（Bearer 生态）
	// 协议认证头：Anthropic 出口 x-api-key、Gemini 出口 x-goog-api-key。
	XAPIKey      string
	XGoogAPIKey  string
	AnthropicVer string
	Body         string
	Header       map[string]string
}

func newTestGateway(t *testing.T) *gateway {
	t.Helper()
	g := &gateway{t: t}

	// 假上游：默认行为按 body 里的标记由各测试通过 upstreamHandler 定制。
	var upstreamHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.upReqs = append(g.upReqs, upReqSnapshot{
			Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(body),
			XAPIKey:      r.Header.Get("x-api-key"),
			XGoogAPIKey:  r.Header.Get("x-goog-api-key"),
			AnthropicVer: r.Header.Get("anthropic-version"),
			Header:       map[string]string{"Content-Type": r.Header.Get("Content-Type")},
		})
		g.mu.Unlock()
		upstreamBehavior(w, r.URL.RequestURI(), string(body))
	}
	g.upstream = httptest.NewServer(upstreamHandler)
	t.Cleanup(g.upstream.Close)

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("开库: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	g.st = st

	if _, err := st.UpsertProvider(t.Context(), store.Provider{
		Name: "fake", Kind: "openai-compat",
		BaseURL: g.upstream.URL + "/v1", APIKey: "sk-upstream", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 provider: %v", err)
	}

	srv := httptest.NewServer(httpapi.New(httpapi.Options{
		Store: st, Log: discardLogger(), AdminToken: "admin-tok",
	}).Handler())
	t.Cleanup(srv.Close)
	g.url = srv.URL
	return g
}

// upstreamBehavior 由各测试覆盖；默认实现：流式返回 SSE，非流式返回 JSON。
var upstreamBehavior = defaultBehavior

func defaultBehavior(w http.ResponseWriter, uri, body string) {
	if strings.Contains(body, `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"choices":[{"delta":{"content":"你"}}]}`,
			`data: {"choices":[{"delta":{"content":"好"}}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
			`data: [DONE]`,
		} {
			io.WriteString(w, chunk+"\n\n")
			f.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"chatcmpl-x","object":"chat.completion",`+
		`"choices":[{"message":{"role":"assistant","content":"好的"}}],`+
		`"usage":{"prompt_tokens":5,"completion_tokens":3}}`)
}

func (g *gateway) lastUpReq() upReqSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.upReqs) == 0 {
		g.t.Fatal("假上游未收到任何请求")
	}
	return g.upReqs[len(g.upReqs)-1]
}

func (g *gateway) newVKey(app string) string {
	g.t.Helper()
	key, _, err := g.st.CreateVKey(g.t.Context(), app)
	if err != nil {
		g.t.Fatalf("签发 vkey: %v", err)
	}
	return key
}

func (g *gateway) do(method, path, key string, body []byte) (*http.Response, string) {
	g.t.Helper()
	if key == "" {
		return g.doRaw(method, path, nil, body)
	}
	return g.doWithHeader(method, path, "Authorization", "Bearer "+key, body)
}

// doWithHeader 用指定头发凭证（x-api-key / x-goog-api-key 生态的 SDK
// 不发 Authorization，测试需要覆盖三种入口形态）。
func (g *gateway) doWithHeader(method, path, header, value string, body []byte) (*http.Response, string) {
	g.t.Helper()
	return g.doRaw(method, path, map[string]string{header: value}, body)
}

func (g *gateway) doRaw(method, path string, headers map[string]string, body []byte) (*http.Response, string) {
	g.t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, g.url+path, rdr)
	if err != nil {
		g.t.Fatalf("构造请求: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
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

func (g *gateway) usageRows() []store.UsageRow {
	rows, err := g.st.UsageByVKey(g.t.Context(), 0, 0)
	if err != nil {
		g.t.Fatalf("聚合: %v", err)
	}
	return rows
}

func TestAuthFailures(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	body := []byte(`{"model":"fake/m","messages":[{"role":"user","content":"hi"}]}`)

	for name, key := range map[string]string{
		"缺少 Bearer": "",
		"错误 key":    "vk-000000000000000000000000000000000000000000000000",
	} {
		resp, out := g.do("POST", "/v1/chat/completions", key, body)
		if resp.StatusCode != 401 {
			t.Fatalf("%s: 期望 401，得到 %d %s", name, resp.StatusCode, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Fatalf("%s: 应为 OpenAI 风格错误对象: %s", name, out)
		}
	}
}

func TestChatNonStream(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	key := g.newVKey("app-a")

	body := []byte(`{"model":"fake/fake-chat","messages":[{"role":"user","content":"你好"}],` +
		`"temperature":0.5,"custom":{"x":1}}`)
	resp, out := g.do("POST", "/v1/chat/completions", key, body)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200: %d %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, `"prompt_tokens":5`) {
		t.Fatalf("响应体应原样透传: %s", out)
	}

	up := g.lastUpReq()
	if up.Path != "/v1/chat/completions" {
		t.Fatalf("上游路径错误: %s", up.Path)
	}
	if up.Auth != "Bearer sk-upstream" {
		t.Fatalf("上游应收到真实 key: %s", up.Auth)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(up.Body), &m); err != nil {
		t.Fatalf("上游 body 非法 JSON: %s", up.Body)
	}
	if m["model"] != "fake-chat" {
		t.Fatalf("model 前缀应被剥掉: %v", m["model"])
	}
	if m["custom"] == nil || m["temperature"] != 0.5 {
		t.Fatalf("未知字段应保留: %s", up.Body)
	}
	if !strings.Contains(up.Body, "你好") {
		t.Fatalf("中文不应被转义: %s", up.Body)
	}

	rows := g.usageRows()
	if len(rows) != 1 || rows[0].InputTokens != 5 || rows[0].OutputTokens != 3 {
		t.Fatalf("usage 落库错误: %+v", rows)
	}
}

func TestChatStream(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	key := g.newVKey("app-s")

	body := []byte(`{"model":"fake/fake-chat","stream":true,` +
		`"messages":[{"role":"user","content":"hi"}]}`)
	resp, out := g.do("POST", "/v1/chat/completions", key, body)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 SSE: %s", ct)
	}
	if !strings.Contains(out, "data: [DONE]") || strings.Count(out, "data: ") != 4 {
		t.Fatalf("SSE 应逐块透传（4 个 data 行）:\n%s", out)
	}
	// 上游应收到注入的 stream_options。
	if !strings.Contains(g.lastUpReq().Body, `"include_usage":true`) {
		t.Fatalf("未注入 stream_options: %s", g.lastUpReq().Body)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 7 || rows[0].OutputTokens != 2 {
		t.Fatalf("流式 usage 抓取错误: %+v", rows)
	}
}

func TestChatStreamOptionsFallback(t *testing.T) {
	g := newTestGateway(t)
	// 上游不认识 stream_options：看到它就 400，去掉后正常出 SSE。
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		if strings.Contains(body, "stream_options") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"Unknown parameter: stream_options","type":"invalid_request_error"}}`)
			return
		}
		defaultBehavior(w, uri, body)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("app-f")

	resp, out := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/fake-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("回退重发应成功: %d %s", resp.StatusCode, out)
	}
	// 两次上游调用：第一次带注入，第二次剥掉。
	g.mu.Lock()
	n := len(g.upReqs)
	g.mu.Unlock()
	if n != 2 {
		t.Fatalf("应重发一次（共 2 次上游请求），实际 %d", n)
	}
}

func TestChatUpstreamErrorPassthrough(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("app-e")

	resp, out := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/m","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 429 || !strings.Contains(out, "rate limited") {
		t.Fatalf("上游错误应原样透传: %d %s", resp.StatusCode, out)
	}
	rows := g.usageRows()
	if rows[0].ErrorCount != 1 || rows[0].Requests != 1 {
		t.Fatalf("失败也应记账: %+v", rows)
	}
}

func TestChatUnregisteredModel(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	key := g.newVKey("app-u")

	// 未登记模型直接透传（登记册不是白名单）。
	resp, _ := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"fake/whatever-new-model","messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("未登记模型应透传: %d", resp.StatusCode)
	}
	if !strings.Contains(g.lastUpReq().Body, `"whatever-new-model"`) {
		t.Fatalf("透传的 model 名错误: %s", g.lastUpReq().Body)
	}
}

func TestChatBadModel(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = defaultBehavior
	key := g.newVKey("app-b")

	for _, model := range []string{"no-prefix", "/x", "x/", ""} {
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{}})
		resp, out := g.do("POST", "/v1/chat/completions", key, body)
		if resp.StatusCode != 400 {
			t.Fatalf("model=%q 期望 400，得到 %d %s", model, resp.StatusCode, out)
		}
	}
	resp, _ := g.do("POST", "/v1/chat/completions", key,
		[]byte(`{"model":"nosuch/m","messages":[]}`))
	if resp.StatusCode != 404 {
		t.Fatalf("未登记 provider 期望 404，得到 %d", resp.StatusCode)
	}
}

func TestModelsList(t *testing.T) {
	g := newTestGateway(t)
	if _, err := g.st.UpsertModel(t.Context(), store.ModelInput{Provider: "fake", Name: "fake-chat", UpstreamModel: "fake-chat-v3"}); err != nil {
		t.Fatalf("登记模型: %v", err)
	}
	key := g.newVKey("app-m")
	resp, out := g.do("GET", "/v1/models", key, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200: %d %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, `"id":"fake/fake-chat"`) {
		t.Fatalf("模型列表应含 fake/fake-chat: %s", out)
	}
}

func TestSystemOne(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-latest","answers":{"billing":{"type":"noul","noul":0.98}},`+
			`"usage":{"input_tokens":120,"output_tokens":12}}`)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	if _, err := g.st.UpsertProvider(t.Context(), store.Provider{
		Name: "typesafe", Kind: "systemone",
		BaseURL: g.upstream.URL + "/v1", APIKey: "ts-key", Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 systemone provider: %v", err)
	}
	key := g.newVKey("app-jev")

	// model 带前缀：应剥掉再转发。
	resp, out := g.do("POST", "/v1/systemone", key, []byte(
		`{"model":"typesafe/jev-latest","state":"Please refund","questions":{"billing":{"type":"noul","instructions":"Is this about billing?"}}}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"noul":0.98`) {
		t.Fatalf("systemone 透传失败: %d %s", resp.StatusCode, out)
	}
	up := g.lastUpReq()
	if up.Path != "/v1/systemone" || up.Auth != "Bearer ts-key" {
		t.Fatalf("上游路径/认证错误: %s %s", up.Path, up.Auth)
	}
	if !strings.Contains(up.Body, `"model":"jev-latest"`) {
		t.Fatalf("model 前缀应剥离: %s", up.Body)
	}
	if !strings.Contains(up.Body, `"state":"Please refund"`) {
		t.Fatalf("state 应透传: %s", up.Body)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 120 || rows[0].OutputTokens != 12 {
		t.Fatalf("systemone usage 落库错误: %+v", rows)
	}
}

func TestHealthz(t *testing.T) {
	g := newTestGateway(t)
	resp, out := g.do("GET", "/healthz", "", nil)
	if resp.StatusCode != 200 || out != "ok" {
		t.Fatalf("healthz: %d %q", resp.StatusCode, out)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

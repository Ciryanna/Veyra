package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/store"
)

// responsesBehavior 假上游的 OpenAI Responses 行为。
func responsesBehavior(w http.ResponseWriter, uri, body string) {
	if strings.Contains(body, `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			"event: response.output_text.delta\n" +
				`data: {"type":"response.output_text.delta","delta":"Hi"}` + "\n\n",
			"event: response.completed\n" +
				`data: {"type":"response.completed","response":{"usage":{"input_tokens":90,"output_tokens":17}}}` + "\n\n",
		} {
			io.WriteString(w, chunk)
			f.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed",`+
		`"output":[],"usage":{"input_tokens":90,"output_tokens":17,"total_tokens":107}}`)
}

func TestResponsesNonStream(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = responsesBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("resp-app")

	resp, out := g.do("POST", "/v1/responses", key,
		[]byte(`{"model":"fake/gpt-5","input":"hi"}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"input_tokens":90`) {
		t.Fatalf("responses 非流式失败: %d %s", resp.StatusCode, out)
	}

	up := g.lastUpReq()
	if up.Path != "/v1/responses" {
		t.Fatalf("上游路径错误: %s", up.Path)
	}
	if up.Auth != "Bearer sk-upstream" {
		t.Fatalf("上游应收到 Bearer key: %q", up.Auth)
	}
	if !strings.Contains(up.Body, `"model":"gpt-5"`) {
		t.Fatalf("model 前缀应剥掉: %s", up.Body)
	}
	// Responses 透传不注入 stream_options。
	if strings.Contains(up.Body, "stream_options") {
		t.Fatalf("responses 不应注入 stream_options: %s", up.Body)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 90 || rows[0].OutputTokens != 17 {
		t.Fatalf("responses usage 落库错误: %+v", rows)
	}
}

func TestResponsesStream(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = responsesBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("resp-s")

	resp, out := g.do("POST", "/v1/responses", key,
		[]byte(`{"model":"fake/gpt-5","input":"hi","stream":true}`))
	if resp.StatusCode != 200 || !strings.Contains(out, "response.completed") {
		t.Fatalf("responses 流式失败: %d %s", resp.StatusCode, out)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 90 || rows[0].OutputTokens != 17 {
		t.Fatalf("流式 usage 错误: %+v", rows)
	}
}

func TestResponsesEgressCascade(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = responsesBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	// primary 上 fake 是 overseas 占位（newEgressTopology 里 egress 侧的 fake
	// provider 已指向真实假上游——直接复用即可）。
	key := primary.newVKey("app-resp")
	resp, out := primary.do("POST", "/v1/responses", key,
		[]byte(`{"model":"fake/gpt-5","input":"hi"}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"input_tokens":90`) {
		t.Fatalf("responses 级联失败: %d %s", resp.StatusCode, out)
	}
	// egress 收到完整 model 名（fake/gpt-5），剥前缀后转给上游。
	if !strings.Contains(egress.lastUpReq().Body, `"model":"gpt-5"`) {
		t.Fatalf("egress 应剥前缀转发: %s", egress.lastUpReq().Body)
	}
	pRows, _ := primary.st.UsageByVKey(t.Context(), 0, 0)
	if pRows[0].InputTokens != 90 {
		t.Fatalf("primary 记账错误: %+v", pRows)
	}
}

func TestResponsesUpstreamErrorPassthrough(t *testing.T) {
	g := newTestGateway(t)
	upstreamBehavior = func(w http.ResponseWriter, uri, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"Unknown endpoint","type":"invalid_request_error"}}`)
	}
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("resp-e")

	// kind 不校验：上游不支持 /responses 的 404 原样透传。
	resp, out := g.do("POST", "/v1/responses", key,
		[]byte(`{"model":"fake/gpt-5","input":"hi"}`))
	if resp.StatusCode != 404 || !strings.Contains(out, "Unknown endpoint") {
		t.Fatalf("上游错误应透传: %d %s", resp.StatusCode, out)
	}
}

// ---- Gemini ----

// geminiBehavior 假上游的 Gemini 行为：按 URI 分派（SSE / 单对象 / 数组）。
func geminiBehavior(w http.ResponseWriter, uri, body string) {
	if strings.Contains(uri, "alt=sse") {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			`data: {"candidates":[{"content":{"parts":[{"text":"你"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`,
			`data: {"candidates":[{"content":{"parts":[{"text":"好"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}`,
		} {
			io.WriteString(w, chunk+"\n\n")
			f.Flush()
		}
		return
	}
	if strings.Contains(uri, "streamGenerateContent") {
		// 无 alt=sse 的流式：chunked JSON 数组。
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}},`+
			`{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}]`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"好的"}]}}],`+
		`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`)
}

func newGeminiGateway(t *testing.T) *gateway {
	t.Helper()
	g := newTestGateway(t)
	if _, err := g.st.UpsertProvider(t.Context(), store.Provider{
		Name: "gemini", Kind: "gemini",
		BaseURL: g.upstream.URL + "/v1beta", APIKey: "goog-real-key", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 gemini provider: %v", err)
	}
	return g
}

func TestGeminiNonStream(t *testing.T) {
	g := newGeminiGateway(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("gem-app")

	// Gemini SDK 风格：x-goog-api-key 头发 vkey，model 带 provider 前缀在 URL 里。
	resp, out := g.doWithHeader("POST", "/v1beta/models/gemini/gemini-2.5-pro:generateContent",
		"x-goog-api-key", key,
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"promptTokenCount":10`) {
		t.Fatalf("gemini 非流式失败: %d %s", resp.StatusCode, out)
	}

	up := g.lastUpReq()
	if up.Path != "/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("上游路径错误（应剥 provider 前缀）: %s", up.Path)
	}
	if up.XGoogAPIKey != "goog-real-key" {
		t.Fatalf("上游应经 x-goog-api-key 收到真实 key: %q", up.XGoogAPIKey)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 10 || rows[0].OutputTokens != 20 {
		t.Fatalf("gemini usage 落库错误: %+v", rows)
	}
}

func TestGeminiNoPrefixRoutesToSoleProvider(t *testing.T) {
	g := newGeminiGateway(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("gem-np")

	// 不带前缀：路由到唯一 enabled gemini provider（与 systemone 同构）。
	resp, _ := g.doWithHeader("POST", "/v1beta/models/gemini-2.5-pro:generateContent",
		"x-goog-api-key", key,
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("无前缀路由失败: %d", resp.StatusCode)
	}
	if g.lastUpReq().Path != "/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("上游路径错误: %s", g.lastUpReq().Path)
	}
}

func TestGeminiStreamSSE(t *testing.T) {
	g := newGeminiGateway(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("gem-s")

	resp, out := g.doWithHeader("POST", "/v1beta/models/gemini/gemini-2.5-pro:streamGenerateContent?alt=sse",
		"x-goog-api-key", key,
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"candidatesTokenCount":20`) {
		t.Fatalf("gemini SSE 流式失败: %d %s", resp.StatusCode, out)
	}
	// 累计语义：末 chunk 的 usageMetadata 胜出。
	rows := g.usageRows()
	if rows[0].InputTokens != 10 || rows[0].OutputTokens != 20 {
		t.Fatalf("SSE 累计 usage 错误: %+v", rows)
	}
}

func TestGeminiStreamJSONArray(t *testing.T) {
	g := newGeminiGateway(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("gem-arr")

	// 无 alt=sse 的流式：JSON 数组整读透传，usage 取末元素。
	resp, out := g.doWithHeader("POST", "/v1beta/models/gemini/gemini-2.5-pro:streamGenerateContent",
		"x-goog-api-key", key,
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if resp.StatusCode != 200 || !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("JSON 数组流失败: %d %s", resp.StatusCode, out)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 10 || rows[0].OutputTokens != 20 {
		t.Fatalf("数组 usage 错误: %+v", rows)
	}
}

func TestGeminiKindMismatch(t *testing.T) {
	g := newGeminiGateway(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("gem-x")

	// fake 是 openai-compat，走 gemini 端点应被拒。
	resp, out := g.doWithHeader("POST", "/v1beta/models/fake/m:generateContent",
		"x-goog-api-key", key, []byte(`{}`))
	if resp.StatusCode != 400 || !strings.Contains(out, "provider_kind_mismatch") {
		t.Fatalf("kind 不匹配应 400: %d %s", resp.StatusCode, out)
	}
}

func TestGeminiEgressCascade(t *testing.T) {
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = geminiBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	if _, err := egress.st.UpsertProvider(t.Context(), store.Provider{
		Name: "gemini", Kind: "gemini",
		BaseURL: egress.upstream.URL + "/v1beta", APIKey: "goog-real-key",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("egress 登记 gemini: %v", err)
	}
	if _, err := primary.st.UpsertProvider(t.Context(), store.Provider{
		Name: "gemini", Kind: "gemini",
		BaseURL: "http://placeholder.invalid/v1beta", APIKey: "placeholder",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("primary 登记占位: %v", err)
	}

	key := primary.newVKey("app-gem")
	// 级联：原始 rest（含前缀）原样转发给 egress，egress 自己解析剥前缀。
	resp, out := primary.doWithHeader("POST", "/v1beta/models/gemini/gemini-2.5-pro:generateContent",
		"x-goog-api-key", key,
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"promptTokenCount":10`) {
		t.Fatalf("gemini 级联失败: %d %s", resp.StatusCode, out)
	}
	if egress.lastUpReq().Path != "/v1beta/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("egress 应剥前缀转发: %s", egress.lastUpReq().Path)
	}
	if egress.lastUpReq().XGoogAPIKey != "goog-real-key" {
		t.Fatalf("上游应经 x-goog-api-key 收到真实 key: %q", egress.lastUpReq().XGoogAPIKey)
	}
}

func TestGeminiModelsList(t *testing.T) {
	g := newGeminiGateway(t)
	if _, err := g.st.UpsertModel(g.t.Context(), store.ModelInput{Provider: "gemini", Name: "gemini-2.5-pro"}); err != nil {
		t.Fatalf("登记模型: %v", err)
	}
	key := g.newVKey("gem-l")
	resp, out := g.doWithHeader("GET", "/v1beta/models", "x-goog-api-key", key, nil)
	if resp.StatusCode != 200 || !strings.Contains(out, `"models/gemini-2.5-pro"`) {
		t.Fatalf("gemini 模型列表异常: %d %s", resp.StatusCode, out)
	}
}

package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/store"
)

// anthropicBehavior 假上游的 Anthropic Messages 行为：
// 流式回 SSE 事件流，非流式回 message JSON。
func anthropicBehavior(w http.ResponseWriter, uri, body string) {
	if strings.Contains(body, `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, chunk := range []string{
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}` + "\n\n",
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}` + "\n\n",
			"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}` + "\n\n",
			"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n",
		} {
			io.WriteString(w, chunk)
			f.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant",`+
		`"content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":25,"output_tokens":15}}`)
}

func newAnthropicGateway(t *testing.T) *gateway {
	t.Helper()
	g := newTestGateway(t)
	if _, err := g.st.UpsertProvider(t.Context(), store.Provider{
		Name: "claude", Kind: "anthropic",
		BaseURL: g.upstream.URL + "/v1", APIKey: "sk-ant-real", Region: "domestic", Enabled: true,
	}); err != nil {
		t.Fatalf("登记 anthropic provider: %v", err)
	}
	return g
}

func TestMessagesNonStream(t *testing.T) {
	g := newAnthropicGateway(t)
	upstreamBehavior = anthropicBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("claude-app")

	// Anthropic SDK 风格：x-api-key 头发 vkey。
	resp, out := g.doWithHeader("POST", "/v1/messages", "x-api-key", key,
		[]byte(`{"model":"claude/claude-sonnet-4-5","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"input_tokens":25`) {
		t.Fatalf("messages 非流式失败: %d %s", resp.StatusCode, out)
	}

	up := g.lastUpReq()
	if up.Path != "/v1/messages" {
		t.Fatalf("上游路径错误: %s", up.Path)
	}
	// 上游认证是 x-api-key（非 Bearer）。
	if up.XAPIKey != "sk-ant-real" {
		t.Fatalf("上游应经 x-api-key 收到真实 key: %q", up.XAPIKey)
	}
	if up.AnthropicVer == "" {
		t.Fatal("上游应收到 anthropic-version 头")
	}
	if !strings.Contains(up.Body, `"model":"claude-sonnet-4-5"`) {
		t.Fatalf("model 前缀应剥掉: %s", up.Body)
	}
	rows := g.usageRows()
	if rows[0].InputTokens != 25 || rows[0].OutputTokens != 15 {
		t.Fatalf("messages usage 落库错误: %+v", rows)
	}
}

func TestMessagesStream(t *testing.T) {
	g := newAnthropicGateway(t)
	upstreamBehavior = anthropicBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("claude-s")

	resp, out := g.doWithHeader("POST", "/v1/messages", "x-api-key", key,
		[]byte(`{"model":"claude/claude-sonnet-4-5","max_tokens":100,"stream":true,`+
			`"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, "event: message_stop") {
		t.Fatalf("messages 流式失败: %d %s", resp.StatusCode, out)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 SSE: %s", ct)
	}
	rows := g.usageRows()
	// message_start 的 input 与 message_delta 的 output 字段级合并。
	if rows[0].InputTokens != 25 || rows[0].OutputTokens != 15 {
		t.Fatalf("流式合并 usage 错误: %+v", rows)
	}
}

func TestMessagesBearerAuthAlsoWorks(t *testing.T) {
	g := newAnthropicGateway(t)
	upstreamBehavior = anthropicBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("claude-b")

	// OpenAI 风格 Bearer 也接受（同一 vkey 空间）。
	resp, _ := g.do("POST", "/v1/messages", key,
		[]byte(`{"model":"claude/claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("Bearer 认证应可用: %d", resp.StatusCode)
	}
}

func TestMessagesKindMismatch(t *testing.T) {
	g := newAnthropicGateway(t)
	upstreamBehavior = anthropicBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })
	key := g.newVKey("claude-x")

	// fake provider 是 openai-compat，走 messages 应被拒。
	resp, out := g.do("POST", "/v1/messages", key,
		[]byte(`{"model":"fake/m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 400 || !strings.Contains(out, "provider_kind_mismatch") {
		t.Fatalf("kind 不匹配应 400: %d %s", resp.StatusCode, out)
	}
}

func TestMessagesEgressCascade(t *testing.T) {
	// egress 拓扑：egress 侧登记真实 anthropic provider，primary 侧只放占位。
	primary, egress, _ := newEgressTopology(t)
	upstreamBehavior = anthropicBehavior
	t.Cleanup(func() { upstreamBehavior = defaultBehavior })

	if _, err := egress.st.UpsertProvider(t.Context(), store.Provider{
		Name: "claude", Kind: "anthropic",
		BaseURL: egress.upstream.URL + "/v1", APIKey: "sk-ant-real",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("egress 登记 anthropic: %v", err)
	}
	if _, err := primary.st.UpsertProvider(t.Context(), store.Provider{
		Name: "claude", Kind: "anthropic",
		BaseURL: "http://placeholder.invalid/v1", APIKey: "placeholder",
		Region: "overseas", Enabled: true,
	}); err != nil {
		t.Fatalf("primary 登记占位: %v", err)
	}

	key := primary.newVKey("app-msg")
	resp, out := primary.doWithHeader("POST", "/v1/messages", "x-api-key", key,
		[]byte(`{"model":"claude/claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	if resp.StatusCode != 200 || !strings.Contains(out, `"input_tokens":25`) {
		t.Fatalf("messages 级联失败: %d %s", resp.StatusCode, out)
	}
	// egress 转给真实上游时用 x-api-key。
	if egress.lastUpReq().XAPIKey != "sk-ant-real" {
		t.Fatalf("上游应经 x-api-key 收到真实 key: %q", egress.lastUpReq().XAPIKey)
	}
	// 两跳各自记账。
	pRows, _ := primary.st.UsageByVKey(t.Context(), 0, 0)
	if pRows[0].InputTokens != 25 {
		t.Fatalf("primary 记账错误: %+v", pRows)
	}
}

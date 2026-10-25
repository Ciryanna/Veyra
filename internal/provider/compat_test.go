package provider_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/provider"
)

func TestRewriteAndInject(t *testing.T) {
	body := []byte(`{"model":"deepseek/deepseek-chat","stream":true,` +
		`"messages":[{"role":"user","content":"你好"}],"temperature":0.7,"custom_field":{"x":[1,2,3]}}`)
	m, err := provider.ParseChatBody(body)
	if err != nil {
		t.Fatalf("解析: %v", err)
	}
	provider.RewriteModel(m, "deepseek-chat-v3")
	provider.InjectStreamUsage(m)
	out, err := provider.MarshalBody(m)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	m2, err := provider.ParseChatBody(out)
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if m2["model"] != "deepseek-chat-v3" {
		t.Fatalf("model 未重写: %v", m2["model"])
	}
	so, _ := m2["stream_options"].(map[string]any)
	if v, _ := so["include_usage"].(bool); !v {
		t.Fatalf("stream_options.include_usage 未注入: %v", m2["stream_options"])
	}
	// 未知字段必须原样保留——透传不丢信息。
	if m2["custom_field"] == nil || m2["temperature"] != 0.7 {
		t.Fatalf("未知字段丢失: %v", m2)
	}
	// 中文不转义成 \uXXXX。
	if !strings.Contains(string(out), "你好") {
		t.Fatalf("中文被 HTML 转义: %s", out)
	}
}

func TestInjectRespectExplicit(t *testing.T) {
	m, _ := provider.ParseChatBody([]byte(
		`{"model":"x","stream":true,"stream_options":{"include_usage":false}}`))
	provider.InjectStreamUsage(m)
	so, _ := m["stream_options"].(map[string]any)
	if v, _ := so["include_usage"].(bool); v {
		t.Fatal("客户端显式 false 不应被覆盖")
	}
	provider.StripStreamUsage(m)
	if _, ok := m["stream_options"]; ok {
		t.Fatal("Strip 未删除 stream_options")
	}
}

func TestExtractUsage(t *testing.T) {
	u := provider.ExtractChatUsage([]byte(
		`{"id":"x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
	if u.Input != 7 || u.Output != 2 || u.Total() != 9 {
		t.Fatalf("chat usage 错误: %+v", u)
	}
	if u := provider.ExtractChatUsage([]byte(`{"choices":[]}`)); u.Total() != 0 {
		t.Fatalf("无 usage 应为零值: %+v", u)
	}

	su := provider.ExtractSystemOneUsage([]byte(
		`{"model":"jev-latest","answers":{},"usage":{"input_tokens":120,"output_tokens":12}}`))
	if su.Input != 120 || su.Output != 12 {
		t.Fatalf("systemone usage 错误: %+v", su)
	}
}

func TestStreamSSE(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	u, err := provider.StreamSSE(rec, strings.NewReader(sse), provider.OpenAIChunkUsage)
	if err != nil {
		t.Fatalf("转发: %v", err)
	}
	if rec.Body.String() != sse {
		t.Fatalf("SSE 未逐字透传:\n%q\n%q", sse, rec.Body.String())
	}
	if u.Input != 7 || u.Output != 2 {
		t.Fatalf("usage 抓取错误: %+v", u)
	}
}

func TestStripProviderPrefix(t *testing.T) {
	cases := map[string]string{
		"typesafe/jev-latest": "jev-latest",
		"jev-latest":          "jev-latest",
		"a/b/c":               "b/c",
	}
	for in, want := range cases {
		if got := provider.StripProviderPrefix(in); got != want {
			t.Fatalf("StripProviderPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

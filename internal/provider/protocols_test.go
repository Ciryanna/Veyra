package provider_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ciryanna/Veyra/internal/provider"
)

func TestExtractAnthropicUsage(t *testing.T) {
	u := provider.ExtractAnthropicUsage([]byte(
		`{"id":"msg_1","type":"message","content":[],"usage":{"input_tokens":25,"output_tokens":150}}`))
	if u.Input != 25 || u.Output != 150 {
		t.Fatalf("usage 错误: %+v", u)
	}
	if u := provider.ExtractAnthropicUsage([]byte(`{"type":"message"}`)); u.Total() != 0 {
		t.Fatalf("无 usage 应为零值: %+v", u)
	}
}

func TestAnthropicSSEExtractor(t *testing.T) {
	ex := provider.NewAnthropicSSEExtractor()
	// message_start 给 input，中间事件零值，message_delta 给累计 output。
	if u := ex(`{"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`); u.Input != 25 || u.Output != 0 {
		t.Fatalf("message_start 应只有 input: %+v", u)
	}
	if u := ex(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`); u.Total() != 0 {
		t.Fatalf("中间事件应零值: %+v", u)
	}
	if u := ex(`{"type":"message_delta","delta":{},"usage":{"output_tokens":15}}`); u.Input != 0 || u.Output != 15 {
		t.Fatalf("message_delta 应只有 output: %+v", u)
	}
	if u := ex(`{"type":"message_stop"}`); u.Total() != 0 {
		t.Fatalf("message_stop 应零值: %+v", u)
	}
	if u := ex("[DONE]"); u.Total() != 0 {
		t.Fatal("[DONE] 应零值")
	}
}

func TestStreamSSEAnthropicMerge(t *testing.T) {
	// 字段级合并：message_start 的 input 与 message_delta 的 output 合成完整 usage。
	sse := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{},"usage":{"output_tokens":15}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	rec := httptest.NewRecorder()
	u, err := provider.StreamSSE(rec, strings.NewReader(sse), provider.NewAnthropicSSEExtractor())
	if err != nil {
		t.Fatalf("转发: %v", err)
	}
	if rec.Body.String() != sse {
		t.Fatalf("SSE 未逐字透传")
	}
	if u.Input != 25 || u.Output != 15 {
		t.Fatalf("合并 usage 错误: %+v", u)
	}
}

func TestExtractResponsesUsage(t *testing.T) {
	u := provider.ExtractResponsesUsage([]byte(
		`{"id":"resp_1","object":"response","usage":{"input_tokens":90,"output_tokens":17,"total_tokens":107}}`))
	if u.Input != 90 || u.Output != 17 {
		t.Fatalf("usage 错误: %+v", u)
	}
	if u := provider.ExtractResponsesUsage([]byte(`{"object":"response"}`)); u.Total() != 0 {
		t.Fatalf("无 usage 应零值: %+v", u)
	}
}

func TestResponsesSSEExtractor(t *testing.T) {
	ex := provider.NewResponsesSSEExtractor()
	if u := ex(`{"type":"response.output_text.delta","delta":"Hi"}`); u.Total() != 0 {
		t.Fatalf("中间事件应零值: %+v", u)
	}
	u := ex(`{"type":"response.completed","response":{"usage":{"input_tokens":90,"output_tokens":17}}}`)
	if u.Input != 90 || u.Output != 17 {
		t.Fatalf("completed 事件 usage 错误: %+v", u)
	}
}

func TestStreamSSEResponsesMerge(t *testing.T) {
	sse := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"Hi"}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":90,"output_tokens":17}}}` + "\n\n"

	rec := httptest.NewRecorder()
	u, err := provider.StreamSSE(rec, strings.NewReader(sse), provider.NewResponsesSSEExtractor())
	if err != nil {
		t.Fatalf("转发: %v", err)
	}
	if u.Input != 90 || u.Output != 17 {
		t.Fatalf("usage 错误: %+v", u)
	}
}

func TestExtractGeminiUsage(t *testing.T) {
	// 单对象（非流式 generateContent）。
	u := provider.ExtractGeminiUsage([]byte(
		`{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}`))
	if u.Input != 10 || u.Output != 20 {
		t.Fatalf("单对象 usage 错误: %+v", u)
	}
	// JSON 数组（streamGenerateContent 无 alt=sse）：取末元素终值。
	u = provider.ExtractGeminiUsage([]byte(
		`[{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}},` +
			`{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}]`))
	if u.Input != 10 || u.Output != 20 {
		t.Fatalf("数组 usage 错误: %+v", u)
	}
	if u := provider.ExtractGeminiUsage([]byte(`{"candidates":[]}`)); u.Total() != 0 {
		t.Fatalf("无 usageMetadata 应零值: %+v", u)
	}
}

func TestGeminiSSEExtractor(t *testing.T) {
	ex := provider.GeminiSSEExtractor
	if u := ex(`{"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`); u.Input != 10 || u.Output != 5 {
		t.Fatalf("chunk usage 错误: %+v", u)
	}
	if u := ex(`{"candidates":[]}`); u.Total() != 0 {
		t.Fatalf("无 usageMetadata 应零值: %+v", u)
	}
}

func TestStreamSSEGeminiMerge(t *testing.T) {
	// 累计语义：两个 chunk 的 usageMetadata 后者胜出。
	sse := `data: {"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}` + "\n\n" +
		`data: {"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20}}` + "\n\n"

	rec := httptest.NewRecorder()
	u, err := provider.StreamSSE(rec, strings.NewReader(sse), provider.GeminiSSEExtractor)
	if err != nil {
		t.Fatalf("转发: %v", err)
	}
	if u.Input != 10 || u.Output != 20 {
		t.Fatalf("累计 usage 错误: %+v", u)
	}
}

package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Usage 是一次调用的 token 计数。上游没给就保持 0——
// 刻意不做"字符数/4"之类的估算：掺入估算值会污染聚合统计的可信度，
// 宁可显示为 0 让配置问题可见。
type Usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

func (u Usage) Total() int64 { return u.Input + u.Output }

// SSEUsageExtractor 从单个 SSE data payload 解析 usage（见 StreamSSE）。
// 返回部分值合法，StreamSSE 做字段级合并。
type SSEUsageExtractor func(data string) Usage

// ParseChatBody 解析 chat/completions 请求体（map 而非 struct：保留全部未知字段，
// 透传不丢信息）。数字经 float64 往返在 2^53 内精确，对请求体安全。
func ParseChatBody(body []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	return m, nil
}

// MarshalBody 重新序列化，关闭 HTML 转义（避免中文 content 变 \uXXXX 膨胀流量）。
func MarshalBody(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("序列化请求体: %w", err)
	}
	return buf.Bytes(), nil
}

// RewriteModel 把 model 字段替换为发给上游的名字。
func RewriteModel(m map[string]any, upstreamModel string) { m["model"] = upstreamModel }

// IsStream 报告请求是否要求流式。
func IsStream(m map[string]any) bool {
	v, _ := m["stream"].(bool)
	return v
}

// InjectStreamUsage 注入 stream_options.include_usage=true。
// OpenAI 兼容上游在流式下默认不回 usage，必须显式要；
// 已显式设置为 true/false 的请求不动（尊重客户端意图）。
// 已知代价：不支持 stream_options 的老兼容端点会 400——
// 调用方（httpapi）负责 400 回退重发，见 StripStreamUsage。
func InjectStreamUsage(m map[string]any) {
	so, _ := m["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	if v, ok := so["include_usage"].(bool); ok {
		_ = v // 客户端已显式指定，保持原样
		return
	}
	so["include_usage"] = true
	m["stream_options"] = so
}

// StripStreamUsage 移除注入的 stream_options（400 回退路径专用）。
// 连客户端自带的一起删：既然上游对 stream_options 报 400，
// 它就不认识这个字段，留着同样会炸。
func StripStreamUsage(m map[string]any) { delete(m, "stream_options") }

// ExtractChatUsage 从非流式响应体提取 usage（prompt_tokens/completion_tokens）。
// 解析失败返回零值，调用方继续透传原始 body。
func ExtractChatUsage(body []byte) Usage {
	var r struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Usage{}
	}
	if r.Usage == nil {
		return Usage{}
	}
	return Usage{Input: r.Usage.PromptTokens, Output: r.Usage.CompletionTokens}
}

// ExtractSystemOneUsage 从 /v1/systemone 响应体提取 usage
// （TypeSafe 字段名：input_tokens/output_tokens，结构上与 LLM 一致）。
func ExtractSystemOneUsage(body []byte) Usage {
	var r struct {
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Usage{}
	}
	if r.Usage == nil {
		return Usage{}
	}
	return Usage{Input: r.Usage.InputTokens, Output: r.Usage.OutputTokens}
}

// StreamSSE 把上游 SSE 逐行透传给客户端并合并 usage。
//
// 每读一行立刻写 + Flush（Flusher 可用时），不攒块——攒块会把
// token 间隔变成肉眼可见的卡顿。行缓冲 1MB：reasoning/长工具调用的
// 单 chunk 可以远超 bufio 默认 64KB，截断即协议破坏。
//
// usage 合并语义：extract 对每个 data payload 返回部分 Usage，
// 非零字段覆盖累计值。这对四种协议天然成立：
//   - OpenAI chat：usage 独立 chunk 一次给全（覆盖）
//   - Anthropic：message_start 给 input、message_delta 给 output（各补各的）
//   - Responses：response.completed 一次给全（覆盖）
//   - Gemini：每 chunk 的 usageMetadata 为累计值（持续覆盖，终值胜出）
//
// 客户端中途断开返回已累计值与错误，调用方照常记账。
func StreamSSE(w http.ResponseWriter, body io.Reader, extract SSEUsageExtractor) (Usage, error) {
	flusher, _ := w.(http.Flusher)
	br := bufio.NewReaderSize(body, 1<<20)
	var u Usage
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			if _, werr := io.WriteString(w, line); werr != nil {
				return u, fmt.Errorf("客户端断开: %w", werr)
			}
			if flusher != nil {
				flusher.Flush()
			}
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				if cu := extract(strings.TrimSpace(data)); cu.Input > 0 || cu.Output > 0 {
					if cu.Input > 0 {
						u.Input = cu.Input
					}
					if cu.Output > 0 {
						u.Output = cu.Output
					}
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return u, nil
			}
			return u, fmt.Errorf("读上游 SSE: %w", rerr)
		}
	}
}

// OpenAIChunkUsage 是 OpenAI 兼容流的 extractor：
// [DONE] 与非 JSON 行安静返回零值。
func OpenAIChunkUsage(data string) Usage {
	if data == "" || data == "[DONE]" {
		return Usage{}
	}
	return ExtractChatUsage([]byte(data))
}

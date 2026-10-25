package provider

import "encoding/json"

// OpenAI Responses 协议（POST /v1/responses）。透传优先：请求体只重写 model；
// Bearer 认证；无 stream_options 注入问题（Responses 流式默认回 usage）。
// 非流式 usage 在响应顶层；流式在 response.completed 事件的 response.usage。

// ExtractResponsesUsage 从非流式响应体提取 usage（input_tokens/output_tokens）。
func ExtractResponsesUsage(body []byte) Usage {
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

// NewResponsesSSEExtractor 返回 SSE data 行解析闭包：
// 只认 response.completed 事件（终结事件，usage 一次给全）。
func NewResponsesSSEExtractor() func(data string) Usage {
	return func(data string) Usage {
		if data == "" || data == "[DONE]" {
			return Usage{}
		}
		var ev struct {
			Type     string `json:"type"`
			Response struct {
				Usage struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Usage{}
		}
		if ev.Type == "response.completed" {
			return Usage{Input: ev.Response.Usage.InputTokens, Output: ev.Response.Usage.OutputTokens}
		}
		return Usage{}
	}
}

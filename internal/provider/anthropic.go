package provider

import "encoding/json"

// Anthropic Messages 协议（POST /v1/messages）。透传优先：请求体只重写
// model；认证头 x-api-key + anthropic-version（见 httpapi 的调用点）。
// 流式为 SSE 事件流（event: xxx + data: {...}），usage 分两处出现：
// message_start 携带 input_tokens，message_delta 携带累计 output_tokens。

// ExtractAnthropicUsage 从非流式响应体提取 usage（input_tokens/output_tokens）。
// 解析失败返回零值，调用方继续透传原始 body。
func ExtractAnthropicUsage(body []byte) Usage {
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

// NewAnthropicSSEExtractor 返回有状态的 SSE data 行解析闭包。
// message_start → input；message_delta → output（累计值，后者胜出）；
// 其余事件零值。StreamSSE 对返回值做字段级合并（见 compat.go），
// 部分值天然兼容。
func NewAnthropicSSEExtractor() func(data string) Usage {
	return func(data string) Usage {
		if data == "" || data == "[DONE]" {
			return Usage{}
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return Usage{}
		}
		switch ev.Type {
		case "message_start":
			return Usage{Input: ev.Message.Usage.InputTokens}
		case "message_delta":
			return Usage{Output: ev.Usage.OutputTokens}
		}
		return Usage{}
	}
}

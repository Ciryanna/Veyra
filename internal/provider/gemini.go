package provider

import "encoding/json"

// Gemini 原生协议（POST /v1beta/models/{model}:{method}）。model 在 URL
// 路径里、不在请求体——body 原样透传，模型映射通过改写 URL 实现。
// 认证头 x-goog-api-key。usage 在 usageMetadata：
// promptTokenCount（输入）/ candidatesTokenCount（输出）。
//
// 三种响应形态：
//   - generateContent：单 JSON 对象
//   - streamGenerateContent?alt=sse：SSE data 行（每行完整对象，累计）
//   - streamGenerateContent（无 alt）：chunked JSON 数组 [ {...}, {...} ]

type geminiUsageMeta struct {
	PromptTokens     int64 `json:"promptTokenCount"`
	CandidatesTokens int64 `json:"candidatesTokenCount"`
}

func geminiUsage(m *geminiUsageMeta) Usage {
	if m == nil {
		return Usage{}
	}
	return Usage{Input: m.PromptTokens, Output: m.CandidatesTokens}
}

// ExtractGeminiUsage 兼容单对象与 JSON 数组两形态；
// 数组取末元素（usageMetadata 在 chunk 间累计，末元素即终值）。
func ExtractGeminiUsage(body []byte) Usage {
	var obj struct {
		UsageMetadata *geminiUsageMeta `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &obj); err == nil {
		return geminiUsage(obj.UsageMetadata)
	}
	var arr []struct {
		UsageMetadata *geminiUsageMeta `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &arr); err == nil {
		for i := len(arr) - 1; i >= 0; i-- {
			if arr[i].UsageMetadata != nil {
				return geminiUsage(arr[i].UsageMetadata)
			}
		}
	}
	return Usage{}
}

// GeminiSSEExtractor 无状态：alt=sse 的每个 data 行都是完整响应对象，
// usageMetadata 为累计值，直接返回（StreamSSE 字段级合并后即终值）。
func GeminiSSEExtractor(data string) Usage {
	if data == "" || data == "[DONE]" {
		return Usage{}
	}
	var obj struct {
		UsageMetadata *geminiUsageMeta `json:"usageMetadata"`
	}
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return Usage{}
	}
	return geminiUsage(obj.UsageMetadata)
}

package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SystemOne（TypeSafe Jev）协议：POST /v1/systemone，非流式 JSON 请求-响应。
// 请求 {model, state, questions}，响应 {model, answers, usage}。
// 详见 https://docs.typesafe.ai/api —— Veyra 不改写 questions/state，纯透传。

// ParseSystemOneBody 解析 systemone 请求体，返回 model 字段（可能为空）。
func ParseSystemOneBody(body []byte) (string, map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", nil, fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	model, _ := m["model"].(string)
	return model, m, nil
}

// StripProviderPrefix 去掉 model 的 provider 前缀（"typesafe/jev-latest" → "jev-latest"）。
// 无前缀原样返回——TypeSafe SDK 直连用户不写前缀，网关不该强迫。
func StripProviderPrefix(model string) string {
	if _, rest, ok := strings.Cut(model, "/"); ok {
		return rest
	}
	return model
}

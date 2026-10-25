package httpapi

import (
	"encoding/json"
	"net/http"
)

// writeAPIError 输出 OpenAI 风格错误对象：
// {"error":{"message":...,"type":...,"code":...}}
// 客户端 SDK 均按此结构解析展示，网关自造格式只会两头不讨好。
func (s *Server) writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "veyra_gateway_error",
			"code":    code,
		},
	})
}

// writeJSON 统一 JSON 出口。失败只剩日志可做（连接可能已断）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s := v
		_ = s
	}
}

// passthrough 把上游已应答的错误原样透传：状态码、Content-Type、body 一字不改。
// 网关唯一的增值是记账，不重写错误内容——重写了客户端排查就断在网关这层。
func passthrough(w http.ResponseWriter, status int, contentType string, body []byte) {
	h := w.Header()
	if contentType == "" {
		contentType = "application/json"
	}
	h.Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(body)
}

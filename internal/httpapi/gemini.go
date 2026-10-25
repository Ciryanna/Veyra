package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// handleGemini 透传 Gemini 原生协议（POST /v1beta/models/{rest}）。
//
// rest 形如 "gemini/gemini-2.5-pro:generateContent"（带 provider 前缀）
// 或 "gemini-2.5-pro:generateContent"（不带——路由到唯一 enabled
// gemini provider，与 systemone 同构）。model 在 URL 路径里、不在请求体，
// 请求 body 原样透传；模型映射通过改写出口 URL 实现。
// 认证：入口 x-goog-api-key（Gemini SDK 默认头）填 vkey，
// 出口 x-goog-api-key 填上游真实 key。
//
// 流式形态：streamGenerateContent?alt=sse 是 SSE；无 alt=sse 是
// chunked JSON 数组——前者走逐行透传，后者整读透传（ExtractGeminiUsage
// 能解析数组取终值）。
func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	vk := CurrentVKey(r.Context())
	ev := store.UsageEvent{VKeyID: vk.ID, AppName: vk.AppName, Endpoint: "gemini"}

	rest := r.PathValue("rest")
	pathPart, method, ok := strings.Cut(rest, ":")
	if !ok || pathPart == "" || method == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_path",
			`URL 应为 /v1beta/models/{provider/}model:method`)
		return
	}

	// 解析 provider 前缀：含 "/" 则首段是 provider 名；否则整段是 model，
	// 路由到唯一 enabled gemini provider。
	providerName, modelName := "", pathPart
	if first, remainder, has := strings.Cut(pathPart, "/"); has && first != "" && remainder != "" {
		providerName, modelName = first, remainder
	}
	var p store.Provider
	var err error
	if providerName != "" {
		p, err = s.st.GetProviderByName(r.Context(), providerName)
	} else {
		p, err = s.st.GetGeminiProvider(r.Context())
	}
	if err != nil {
		s.writeAPIError(w, http.StatusNotFound, "provider_not_found",
			"未登记或已禁用的 gemini provider（veyra provider add --kind gemini）")
		return
	}
	if p.Kind != "gemini" {
		s.writeAPIError(w, http.StatusBadRequest, "provider_kind_mismatch",
			"provider "+p.Name+" 是 "+p.Kind+" 类型，/v1beta/models/* 需要 gemini 类型")
		return
	}
	ev.ProviderID = p.ID
	ev.Model = p.Name + "/" + modelName

	upstreamModel := modelName
	if mm, err := s.st.GetModel(r.Context(), p.Name, modelName); err == nil {
		upstreamModel = mm.UpstreamModel
		ev.PriceInNano, ev.PriceOutNano = mm.PriceInNano, mm.PriceOutNano
	}

	// 出口 URL 与认证。级联改道（primary→egress）与 chat 同构：
	// 原始 rest（含前缀）原样转发，egress 自己解析。
	outURL, upKey := provider.GeminiURL(p.BaseURL, upstreamModel, method), p.APIKey
	if s.egressURL != "" && p.Region == "overseas" {
		outURL = s.egressURL + "/v1beta/models/" + rest
		upKey = s.egressKey
	}
	if q := r.URL.RawQuery; q != "" {
		outURL += "?" + q
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxReqBody))
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "read_body", "读取请求体失败: "+err.Error())
		return
	}

	headers := map[string]string{"x-goog-api-key": upKey}
	resp, err := s.pc.DoWithHeaders(r.Context(), http.MethodPost, outURL, headers, body)
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = 0, err.Error(), elapsedMs(start)
		s.recordUsage(r.Context(), ev)
		s.writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()

	// alt=sse → SSE 逐行透传；其余（含 JSON 数组流）整读透传。
	if r.URL.Query().Get("alt") == "sse" {
		s.streamResponse(w, resp, &ev, start, provider.GeminiSSEExtractor)
		return
	}
	s.passthroughJSON(w, resp, &ev, start, provider.ExtractGeminiUsage)
}

// handleGeminiModels 输出 Gemini 格式的模型列表（GET /v1beta/models）。
// 数据来自 gemini provider 的模型登记册；name 为 "models/<name>"。
func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.st.ListModels(r.Context())
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "list_models", err.Error())
		return
	}
	// 只列 gemini provider 的模型；上游真实清单可直连 GET {base}/models 拿。
	providers, err := s.st.ListProviders(r.Context())
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "list_providers", err.Error())
		return
	}
	geminiNames := map[string]bool{}
	for _, p := range providers {
		if p.Kind == "gemini" && p.Enabled {
			geminiNames[p.Name] = true
		}
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if !geminiNames[m.ProviderName] {
			continue
		}
		out = append(out, map[string]any{
			"name": "models/" + m.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

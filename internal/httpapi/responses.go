package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// handleResponses 透传 OpenAI Responses 协议（POST /v1/responses）。
// 客户端用 OpenAI SDK（responses API）/ Codex CLI 直连。
// kind 不校验（openai-compat 即可）：上游支不支持 /responses
// 由上游自己回答——透传优先，网关不做能力臆断。
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	vk := CurrentVKey(r.Context())
	ev := store.UsageEvent{VKeyID: vk.ID, AppName: vk.AppName, Endpoint: "responses"}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxReqBody))
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "read_body", "读取请求体失败: "+err.Error())
		return
	}
	m, err := provider.ParseChatBody(body)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	model, _ := m["model"].(string)
	providerName, modelName, ok := strings.Cut(model, "/")
	if !ok || providerName == "" || modelName == "" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_model",
			`model 需写作 "provider/model"，如 "openai/gpt-5"`)
		return
	}
	ev.Model = model

	p, err := s.st.GetProviderByName(r.Context(), providerName)
	if err != nil {
		s.writeAPIError(w, http.StatusNotFound, "provider_not_found",
			"未登记或已禁用的 provider: "+providerName)
		return
	}
	ev.ProviderID = p.ID

	upstreamModel := modelName
	if mm, err := s.st.GetModel(r.Context(), providerName, modelName); err == nil {
		upstreamModel = mm.UpstreamModel
		ev.PriceInNano, ev.PriceOutNano = mm.PriceInNano, mm.PriceOutNano
	}
	upURL, upKey := provider.ResponsesURL(p.BaseURL), p.APIKey
	if s.egressURL != "" && p.Region == "overseas" {
		upURL = provider.ResponsesURL(s.egressURL + "/v1")
		upKey = s.egressKey
		upstreamModel = model
	}
	provider.RewriteModel(m, upstreamModel)

	upBody, err := provider.MarshalBody(m)
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "marshal", err.Error())
		return
	}

	resp, err := s.pc.Do(r.Context(), http.MethodPost, upURL, upKey, upBody)
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = 0, err.Error(), elapsedMs(start)
		s.recordUsage(r.Context(), ev)
		s.writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()

	// Responses 流式默认回 usage（无 stream_options 注入问题），
	// 直接透传。
	if provider.IsStream(m) {
		s.streamResponse(w, resp, &ev, start, provider.NewResponsesSSEExtractor())
		return
	}
	s.passthroughJSON(w, resp, &ev, start, provider.ExtractResponsesUsage)
}

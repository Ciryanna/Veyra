package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// handleMessages 透传 Anthropic Messages 协议（POST /v1/messages）。
// 客户端用 Claude Code / Anthropic SDK 直连（x-api-key 填 vkey），
// 零转换损耗。上游认证 x-api-key + anthropic-version；
// 级联改道（primary→egress）与 chat 同构。
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	vk := CurrentVKey(r.Context())
	ev := store.UsageEvent{VKeyID: vk.ID, AppName: vk.AppName, Endpoint: "messages"}

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
			`model 需写作 "provider/model"，如 "anthropic/claude-sonnet-4-5"`)
		return
	}
	ev.Model = model

	p, err := s.st.GetProviderByName(r.Context(), providerName)
	if err != nil {
		s.writeAPIError(w, http.StatusNotFound, "provider_not_found",
			"未登记或已禁用的 provider: "+providerName)
		return
	}
	// 协议形态与认证头强绑定：messages 端点只放行 anthropic kind，
	// 用 openai-compat 的 provider 走这里只会得到上游的诡异错误，不如早拒绝。
	if p.Kind != "anthropic" {
		s.writeAPIError(w, http.StatusBadRequest, "provider_kind_mismatch",
			"provider "+providerName+" 是 "+p.Kind+" 类型，/v1/messages 需要 anthropic 类型")
		return
	}
	ev.ProviderID = p.ID

	upstreamModel := modelName
	if mm, err := s.st.GetModel(r.Context(), providerName, modelName); err == nil {
		upstreamModel = mm.UpstreamModel
		ev.PriceInNano, ev.PriceOutNano = mm.PriceInNano, mm.PriceOutNano
	}
	upURL, upKey := provider.MessagesURL(p.BaseURL), p.APIKey
	if s.egressURL != "" && p.Region == "overseas" {
		upURL = provider.MessagesURL(s.egressURL + "/v1")
		upKey = s.egressKey
		upstreamModel = model
	}
	provider.RewriteModel(m, upstreamModel)

	upBody, err := provider.MarshalBody(m)
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "marshal", err.Error())
		return
	}

	// Anthropic 官方强制 anthropic-version 头：透传客户端带来的，
	// 客户端没带（个别非官方 SDK）则补官方当前版本。
	headers := map[string]string{"x-api-key": upKey}
	if v := strings.TrimSpace(r.Header.Get("anthropic-version")); v != "" {
		headers["anthropic-version"] = v
	} else {
		headers["anthropic-version"] = "2023-06-01"
	}

	resp, err := s.pc.DoWithHeaders(r.Context(), http.MethodPost, upURL, headers, upBody)
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = 0, err.Error(), elapsedMs(start)
		s.recordUsage(r.Context(), ev)
		s.writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()

	if provider.IsStream(m) {
		s.streamResponse(w, resp, &ev, start, provider.NewAnthropicSSEExtractor())
		return
	}
	s.passthroughJSON(w, resp, &ev, start, provider.ExtractAnthropicUsage)
}

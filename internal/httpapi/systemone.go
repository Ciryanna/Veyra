package httpapi

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// handleSystemOne 透传 TypeSafe Jev 的 /v1/systemone。
//
// 路由不按 model 前缀：System One 是独立协议形态（非聊天），
// 固定取唯一 enabled 的 kind='systemone' provider。
// 请求里的 model 若写了前缀（"typesafe/jev-latest"）会被剥掉再转发，
// 直连习惯（model="jev-latest"）不受影响。
func (s *Server) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	vk := CurrentVKey(r.Context())
	ev := store.UsageEvent{VKeyID: vk.ID, AppName: vk.AppName, Endpoint: "systemone"}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxReqBody))
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "read_body", "读取请求体失败: "+err.Error())
		return
	}
	model, m, err := provider.ParseSystemOneBody(body)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	ev.Model = model

	p, err := s.st.GetSystemOneProvider(r.Context())
	if err != nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "systemone_not_configured",
			"未配置 systemone 类型的 provider（veyra provider add --kind systemone）")
		return
	}
	ev.ProviderID = p.ID

	// 计费单价：Jev 只收输入费（输出免费），登记 price-in 即可
	// （如 $0.042/Mtok → --price-in 0.042）。未登记则成本记 0。
	if mm, err := s.st.GetModel(r.Context(), p.Name, provider.StripProviderPrefix(model)); err == nil {
		ev.PriceInNano, ev.PriceOutNano = mm.PriceInNano, mm.PriceOutNano
	}

	// 级联改道（primary 节点）：systemone provider 的真实凭证在 egress 侧。
	// model 前缀剥离照旧——egress 对有无前缀都能处理。
	upURL, upKey := provider.SystemOneURL(p.BaseURL), p.APIKey
	if s.egressURL != "" && p.Region == "overseas" {
		upURL = provider.SystemOneURL(s.egressURL + "/v1")
		upKey = s.egressKey
	}

	if stripped := provider.StripProviderPrefix(model); stripped != model {
		m["model"] = stripped
		if body, err = provider.MarshalBody(m); err != nil {
			s.writeAPIError(w, http.StatusInternalServerError, "marshal", err.Error())
			return
		}
	}

	resp, err := s.pc.Do(r.Context(), http.MethodPost, upURL, upKey, body)
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = 0, err.Error(), elapsedMs(start)
		s.recordUsage(r.Context(), ev)
		s.writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBody))
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = resp.StatusCode, err.Error(), elapsedMs(start)
		s.recordUsage(context.Background(), ev)
		s.writeAPIError(w, http.StatusBadGateway, "read_upstream", "读上游响应失败: "+err.Error())
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		u := provider.ExtractSystemOneUsage(respBody)
		ev.InputTokens, ev.OutputTokens = u.Input, u.Output
	} else {
		ev.ErrorMsg = truncateErr(string(respBody))
	}
	ev.StatusCode, ev.DurationMs = resp.StatusCode, elapsedMs(start)
	s.recordUsage(context.Background(), ev)

	passthrough(w, resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

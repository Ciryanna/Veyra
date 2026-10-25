package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// 请求体上限 32MB（多模态 base64 会被打到这个量级）；
// 响应体上限 64MB（非流式长输出）。
const (
	maxReqBody  = 32 << 20
	maxRespBody = 64 << 20
)

// handleChat 是 /v1/chat/completions 的透传内核。
//
// 路由约定：model 写作 "provider/model"（如 deepseek/deepseek-chat），
// 第一个 "/" 之前是 provider 名（providers.name）——OpenRouter 式多级模型名
// （openrouter/anthropic/claude-3.5-sonnet）天然兼容：前缀后的全部内容都是模型名。
// 模型没在 models 表登记也放行（登记册不是白名单），upstream 名默认等于原名。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	vk := CurrentVKey(r.Context())
	ev := store.UsageEvent{VKeyID: vk.ID, AppName: vk.AppName, Endpoint: "chat"}

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
			`model 需写作 "provider/model"，如 "deepseek/deepseek-chat"`)
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

	// 级联改道（primary 节点）：region=overseas 的 provider 真实凭证在 egress 侧，
	// 本地 base_url/api_key 只是占位。此时 model 保留完整原始名（含 provider 前缀），
	// 由 egress 按前缀路由到真实上游——本地模型映射对桥接无意义。
	upstreamModel := modelName
	if mm, err := s.st.GetModel(r.Context(), providerName, modelName); err == nil {
		upstreamModel = mm.UpstreamModel
		ev.PriceInNano, ev.PriceOutNano = mm.PriceInNano, mm.PriceOutNano
	}
	upURL, upKey := provider.ChatURL(p.BaseURL), p.APIKey
	if s.egressURL != "" && p.Region == "overseas" {
		upURL = provider.ChatURL(s.egressURL + "/v1")
		upKey = s.egressKey
		upstreamModel = model
	}
	provider.RewriteModel(m, upstreamModel)

	stream := provider.IsStream(m)
	injected := false
	if stream {
		provider.InjectStreamUsage(m)
		injected = true
	}
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

	// 400 回退：个别 OpenAI 兼容端点不认识 stream_options。
	// 只在错误信息点名 stream_options 时才重发——普通 400（客户端参数错）
	// 重发没有意义，白白翻倍上游流量。
	if resp.StatusCode == http.StatusBadRequest && injected {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if bytes.Contains(bytes.ToLower(errBody), []byte("stream_options")) {
			s.log.Warn("上游不支持 stream_options，去掉注入重发",
				"provider", p.Name, "model", model)
			provider.StripStreamUsage(m)
			if upBody, err = provider.MarshalBody(m); err != nil {
				s.writeAPIError(w, http.StatusInternalServerError, "marshal", err.Error())
				return
			}
			if resp, err = s.pc.Do(r.Context(), http.MethodPost, upURL, upKey, upBody); err != nil {
				ev.StatusCode, ev.ErrorMsg, ev.DurationMs = 0, err.Error(), elapsedMs(start)
				s.recordUsage(r.Context(), ev)
				s.writeAPIError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
				return
			}
		} else {
			ev.StatusCode, ev.DurationMs = resp.StatusCode, elapsedMs(start)
			ev.ErrorMsg = truncateErr(string(errBody))
			s.recordUsage(r.Context(), ev)
			passthrough(w, resp.StatusCode, resp.Header.Get("Content-Type"), errBody)
			return
		}
	}
	defer resp.Body.Close()

	if stream {
		s.streamResponse(w, resp, &ev, start, provider.OpenAIChunkUsage)
		return
	}
	s.passthroughJSON(w, resp, &ev, start, provider.ExtractChatUsage)
}

// recordUsage 同步落一行用量并打业务日志。WAL 下单行 INSERT 是毫秒级，
// 换 goroutine 会引入"进程退出丢账"与并发写竞争，不值。
// 业务日志（app/model/status/tokens）在这里打而不是 access log——
// 认证注入的 context 在日志中间件洋葱的外层，access log 拿不到 app。
//
// 成本在此统一锁定：单价由路由阶段从 models 表带出（见 UsageEvent 注释），
// 无论流式/非流式/错误路径，走这里就一定带上"当时的价格"。
func (s *Server) recordUsage(ctx context.Context, ev store.UsageEvent) {
	ev.CostNano = ev.InputTokens*ev.PriceInNano + ev.OutputTokens*ev.PriceOutNano
	s.log.Info("调用完成",
		"app", ev.AppName, "endpoint", ev.Endpoint, "model", ev.Model,
		"status", ev.StatusCode, "in_tok", ev.InputTokens, "out_tok", ev.OutputTokens,
		"cost_usd", store.NanoToUSD(ev.CostNano),
		"dur_ms", ev.DurationMs, "err", ev.ErrorMsg,
	)
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.st.InsertUsageEvent(c, ev); err != nil {
		s.log.Error("落用量失败", "err", err, "model", ev.Model, "endpoint", ev.Endpoint)
	}
}

func elapsedMs(start time.Time) int64 { return time.Since(start).Milliseconds() }

func truncateErr(msg string) string {
	if len(msg) > 512 {
		return msg[:512] + "...(截断)"
	}
	return msg
}

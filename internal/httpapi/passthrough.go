package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

// passthroughJSON 非流式响应的透传公共路径（chat/messages/responses/gemini
// 共用）：整读上游 body，extract 提 usage，记账，原样回写。
func (s *Server) passthroughJSON(w http.ResponseWriter, resp *http.Response,
	ev *store.UsageEvent, start time.Time, extract func([]byte) provider.Usage) {
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBody))
	if err != nil {
		ev.StatusCode, ev.ErrorMsg, ev.DurationMs = resp.StatusCode, err.Error(), elapsedMs(start)
		s.recordUsage(context.Background(), *ev)
		s.writeAPIError(w, http.StatusBadGateway, "read_upstream", "读上游响应失败: "+err.Error())
		return
	}
	u := extract(respBody)
	ev.InputTokens, ev.OutputTokens = u.Input, u.Output
	ev.StatusCode, ev.DurationMs = resp.StatusCode, elapsedMs(start)
	s.recordUsage(context.Background(), *ev)

	h := w.Header()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Content-Length", strconv.Itoa(len(respBody)))
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// streamResponse SSE 流的透传公共路径：逐行转发 + extractor 抓 usage + 记账。
// 客户端断开也照常记账（已完成的 token 消耗是事实）。
func (s *Server) streamResponse(w http.ResponseWriter, resp *http.Response,
	ev *store.UsageEvent, start time.Time, extract provider.SSEUsageExtractor) {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/event-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)

	u, err := provider.StreamSSE(w, resp.Body, extract)
	ev.InputTokens, ev.OutputTokens = u.Input, u.Output
	ev.StatusCode, ev.DurationMs = resp.StatusCode, elapsedMs(start)
	if err != nil {
		ev.ErrorMsg = err.Error()
	}
	// 用脱离请求生命周期的 context：客户端断开也会触发 r.Context() 取消，
	// 但已完成的调用必须记下账。
	s.recordUsage(context.Background(), *ev)
}

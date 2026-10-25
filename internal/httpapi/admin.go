package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ciryanna/Veyra/internal/store"
)

// adminAuth 管理端令牌认证。令牌来自 VEYRA_ADMIN_TOKEN，
// 恒定时间比较防时序侧信道。空 token 时这组路由根本不注册。
func (s *Server) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, prefix)), []byte(s.adminToken)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"管理令牌无效"}`)
			return
		}
		next(w, r.WithContext(r.Context()))
	}
}

// maskKey 上游 key 脱敏：UI 只需要"能认出是哪把 key"，
// 前 6 后 4 足够，中间永远不可见。
func maskKey(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("*", len(k))
	}
	return k[:6] + "…" + k[len(k)-4:]
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	wrap := func(h http.HandlerFunc) http.HandlerFunc { return s.adminAuth(h) }
	mux.HandleFunc("GET /admin/api/stats", wrap(s.adminStats))
	mux.HandleFunc("GET /admin/api/providers", wrap(s.adminListProviders))
	mux.HandleFunc("PUT /admin/api/providers", wrap(s.adminPutProvider))
	mux.HandleFunc("PATCH /admin/api/providers/enabled", wrap(s.adminPatchProvider))
	mux.HandleFunc("DELETE /admin/api/providers", wrap(s.adminDeleteProvider))
	mux.HandleFunc("GET /admin/api/models", wrap(s.adminListModels))
	mux.HandleFunc("PUT /admin/api/models", wrap(s.adminPutModel))
	mux.HandleFunc("DELETE /admin/api/models", wrap(s.adminDeleteModel))
	mux.HandleFunc("GET /admin/api/vkeys", wrap(s.adminListVKeys))
	mux.HandleFunc("POST /admin/api/vkeys", wrap(s.adminCreateVKey))
	mux.HandleFunc("PATCH /admin/api/vkeys/enabled", wrap(s.adminPatchVKey))
	mux.HandleFunc("DELETE /admin/api/vkeys", wrap(s.adminDeleteVKey))
	mux.HandleFunc("GET /admin/api/usage", wrap(s.adminUsage))
}

// ---- stats ----

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.StatsSince(r.Context(), sinceMillis(r.URL.Query().Get("since")))
	if err != nil {
		s.writeAPIError(w, 500, "stats", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"requests": st.Requests, "input_tokens": st.InputTok,
		"output_tokens": st.OutputTok, "errors": st.Errors, "active_apps": st.ActiveApps,
		"cost_usd": store.NanoToUSD(st.CostNano),
	})
}

// ---- providers ----

func (s *Server) adminListProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.st.ListProviders(r.Context())
	if err != nil {
		s.writeAPIError(w, 500, "list_providers", err.Error())
		return
	}
	out := make([]map[string]any, len(ps))
	for i, p := range ps {
		out[i] = map[string]any{
			"name": p.Name, "kind": p.Kind, "base_url": p.BaseURL,
			"api_key_masked": maskKey(p.APIKey), "region": p.Region, "enabled": p.Enabled,
			"created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) adminPutProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Region  string `json:"region"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeAPIError(w, 400, "invalid_json", err.Error())
		return
	}
	req.Name, req.BaseURL = strings.TrimSpace(req.Name), strings.TrimSpace(req.BaseURL)
	if req.Name == "" || req.BaseURL == "" {
		s.writeAPIError(w, 400, "missing_fields", "name 与 base_url 必填")
		return
	}
	if req.Kind == "" {
		req.Kind = "openai-compat"
	}
	if req.Region == "" {
		req.Region = "domestic"
	}
	switch req.Kind {
	case "openai-compat", "systemone", "anthropic", "gemini":
	default:
		s.writeAPIError(w, 400, "invalid_kind",
			"kind 只接受 openai-compat / systemone / anthropic / gemini")
		return
	}
	if req.Region != "domestic" && req.Region != "overseas" {
		s.writeAPIError(w, 400, "invalid_region", "region 只接受 domestic 或 overseas")
		return
	}
	// api_key 留空 = 保留原 key：编辑连接信息不该强制重填密钥。
	if req.APIKey == "" {
		if old, err := s.st.GetProviderByNameAny(r.Context(), req.Name); err == nil {
			req.APIKey = old.APIKey
		}
	}
	p, err := s.st.UpsertProvider(r.Context(), store.Provider{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL,
		APIKey: req.APIKey, Region: req.Region, Enabled: true,
	})
	if err != nil {
		s.writeAPIError(w, 500, "upsert_provider", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"name": p.Name, "updated_at": p.UpdatedAt})
}

func (s *Server) adminPatchProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil ||
		strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, 400, "invalid_request", "需要 name 与 enabled")
		return
	}
	if err := s.st.SetProviderEnabled(r.Context(), req.Name, req.Enabled); err != nil {
		s.writeAPIError(w, 404, "provider_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) adminDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		s.writeAPIError(w, 400, "missing_name", "name 查询参数必填")
		return
	}
	if err := s.st.DeleteProvider(r.Context(), name); err != nil {
		s.writeAPIError(w, 404, "provider_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- models ----
// 注意：模型名可含 "/"（如 qwen/qwen3-max），一律走 body/query，
// 不用路径通配——{name} 不匹配带斜杠的段。

func (s *Server) adminListModels(w http.ResponseWriter, r *http.Request) {
	ms, err := s.st.ListModels(r.Context())
	if err != nil {
		s.writeAPIError(w, 500, "list_models", err.Error())
		return
	}
	out := make([]map[string]any, len(ms))
	for i, m := range ms {
		out[i] = map[string]any{
			"provider": m.ProviderName, "name": m.Name,
			"upstream_model": m.UpstreamModel, "enabled": m.Enabled,
			// 价格用人类单位（USD/Mtok）返回；0 = 未定价。
			"price_in_per_mtok":  store.NanoToUSD(m.PriceInNano * 1e6),
			"price_out_per_mtok": store.NanoToUSD(m.PriceOutNano * 1e6),
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) adminPutModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider      string `json:"provider"`
		Name          string `json:"name"`
		UpstreamModel string `json:"upstream_model"`
		// USD per Mtok；0/缺省 = 保留原价（store 层"非零即更新"约定）。
		PriceIn  float64 `json:"price_in_per_mtok"`
		PriceOut float64 `json:"price_out_per_mtok"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeAPIError(w, 400, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.Name) == "" {
		s.writeAPIError(w, 400, "missing_fields", "provider 与 name 必填")
		return
	}
	m, err := s.st.UpsertModel(r.Context(), store.ModelInput{
		Provider: req.Provider, Name: req.Name, UpstreamModel: req.UpstreamModel,
		PriceInNano:  store.USDPerMtokToNano(req.PriceIn),
		PriceOutNano: store.USDPerMtokToNano(req.PriceOut),
	})
	if err != nil {
		s.writeAPIError(w, 500, "upsert_model", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":                 true,
		"price_in_per_mtok":  store.NanoToUSD(m.PriceInNano * 1e6),
		"price_out_per_mtok": store.NanoToUSD(m.PriceOutNano * 1e6),
	})
}

func (s *Server) adminDeleteModel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("provider") == "" || q.Get("name") == "" {
		s.writeAPIError(w, 400, "missing_fields", "provider 与 name 查询参数必填")
		return
	}
	if err := s.st.DeleteModel(r.Context(), q.Get("provider"), q.Get("name")); err != nil {
		s.writeAPIError(w, 404, "model_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- vkeys ----

func (s *Server) adminListVKeys(w http.ResponseWriter, r *http.Request) {
	vs, err := s.st.ListVKeys(r.Context())
	if err != nil {
		s.writeAPIError(w, 500, "list_vkeys", err.Error())
		return
	}
	out := make([]map[string]any, len(vs))
	for i, v := range vs {
		out[i] = map[string]any{
			"app_name": v.AppName, "key_prefix": v.KeyPrefix, "enabled": v.Enabled,
			"created_at": v.CreatedAt, "last_used_at": v.LastUsedAt,
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) adminCreateVKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppName string `json:"app_name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil ||
		strings.TrimSpace(req.AppName) == "" {
		s.writeAPIError(w, 400, "invalid_request", "需要 app_name")
		return
	}
	key, v, err := s.st.CreateVKey(r.Context(), req.AppName)
	if err != nil {
		s.writeAPIError(w, 500, "create_vkey", err.Error())
		return
	}
	// 明文 key 只在这一刻返回一次，与 CLI 行为一致。
	writeJSON(w, 201, map[string]any{
		"app_name": v.AppName, "key": key, "key_prefix": v.KeyPrefix,
	})
}

func (s *Server) adminPatchVKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppName string `json:"app_name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil ||
		strings.TrimSpace(req.AppName) == "" {
		s.writeAPIError(w, 400, "invalid_request", "需要 app_name 与 enabled")
		return
	}
	if err := s.st.SetVKeyEnabled(r.Context(), req.AppName, req.Enabled); err != nil {
		s.writeAPIError(w, 404, "vkey_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) adminDeleteVKey(w http.ResponseWriter, r *http.Request) {
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	if app == "" {
		s.writeAPIError(w, 400, "missing_app", "app 查询参数必填")
		return
	}
	if err := s.st.DeleteVKey(r.Context(), app); err != nil {
		s.writeAPIError(w, 404, "vkey_not_found", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- usage ----

func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request) {
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "vkey"
	}
	if by != "vkey" && by != "provider" {
		s.writeAPIError(w, 400, "invalid_by", "by 只接受 vkey 或 provider")
		return
	}
	since := sinceMillis(r.URL.Query().Get("since"))
	var rows []store.UsageRow
	var err error
	if by == "vkey" {
		rows, err = s.st.UsageByVKey(r.Context(), since, 0)
	} else {
		rows, err = s.st.UsageByProvider(r.Context(), since, 0)
	}
	if err != nil {
		s.writeAPIError(w, 500, "usage", err.Error())
		return
	}
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = map[string]any{
			"label": r.Label, "requests": r.Requests, "input_tokens": r.InputTokens,
			"output_tokens": r.OutputTokens, "total_tokens": r.TotalTokens,
			"errors": r.ErrorCount, "avg_duration_ms": r.AvgDurationMs,
			"cost_usd": store.NanoToUSD(r.CostNano),
		}
	}
	writeJSON(w, 200, out)
}

// sinceMillis 解析 "24h"/"7d" 类窗口为起始毫秒；空或非法返回 0（全量）。
// stats 聚合容忍默认全量，非法输入不 500——仪表盘传错参数不该白屏。
func sinceMillis(s string) int64 {
	if s == "" {
		return 0
	}
	var d time.Duration
	if n := len(s); n > 1 && (s[n-1] == 'd' || s[n-1] == 'D') {
		days, err := strconv.Atoi(s[:n-1])
		if err != nil || days <= 0 {
			return 0
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil || d <= 0 {
			return 0
		}
	}
	return time.Now().Add(-d).UnixMilli()
}

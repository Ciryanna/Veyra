// Package httpapi 是 Veyra 的 HTTP 层：对外暴露 OpenAI 兼容入口、
// System One（Jev）入口与模型列表；管理端路由 M2 再挂。
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/Ciryanna/Veyra/internal/provider"
	"github.com/Ciryanna/Veyra/internal/store"
)

type Server struct {
	st         *store.Store
	pc         *provider.Client
	log        *slog.Logger
	adminToken string
	egressURL  string // 非空 = primary 模式：overseas provider 改道至此
	egressKey  string
}

// Options 是唯一构造入口。AdminToken 为空 = 不注册管理路由，
// 网关退化为纯转发面（最小暴露面原则）。EgressURL/EgressKey 成对非空
// 才启用级联改道（primary 节点）。
type Options struct {
	Store      *store.Store
	Log        *slog.Logger
	AdminToken string
	EgressURL  string // 如 http://100.64.0.2:8080（不含 /v1）
	EgressKey  string
}

func New(opts Options) *Server {
	return &Server{
		st:         opts.Store,
		pc:         provider.NewClient(),
		log:        opts.Log,
		adminToken: opts.AdminToken,
		egressURL:  strings.TrimRight(opts.EgressURL, "/"),
		egressKey:  opts.EgressKey,
	}
}

// Handler 装配全部路由。Go 1.22+ 的方法+路径模式直接挂在 ServeMux 上，
// 不引第三方路由——自用网关的路由复杂度撑不起一个依赖。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.vkeyAuth(s.handleChat))
	mux.HandleFunc("POST /v1/messages", s.vkeyAuth(s.handleMessages))
	mux.HandleFunc("POST /v1/responses", s.vkeyAuth(s.handleResponses))
	mux.HandleFunc("POST /v1/systemone", s.vkeyAuth(s.handleSystemOne))
	mux.HandleFunc("GET /v1/models", s.vkeyAuth(s.handleModels))
	mux.HandleFunc("POST /v1beta/models/{rest...}", s.vkeyAuth(s.handleGemini))
	mux.HandleFunc("GET /v1beta/models", s.vkeyAuth(s.handleGeminiModels))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	if s.adminToken != "" {
		s.adminRoutes(mux)
		s.log.Info("管理端已启用", "routes", "/admin/api/*")
	}
	s.webRoutes(mux)
	return s.withLogging(s.withRecover(mux))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ping(r.Context()); err != nil {
		http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

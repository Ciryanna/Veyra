package httpapi

import (
	"net/http"
	"strings"
	"time"
)

// statusRecorder 包装 ResponseWriter 以抓取状态码。
// 必须显式转发 Flush：包装后类型断言 http.Flusher 会失败，
// SSE 的逐块推送就静默退化成攒块——这是包装 ResponseWriter 的经典坑。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "path", r.URL.Path, "recover", rec)
				s.writeAPIError(w, http.StatusInternalServerError, "internal", "内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// 探活与静态资源（UI 的 js/css）不进日志——高频且无观测价值。
		p := r.URL.Path
		if p == "/healthz" || p == "/" || p == "/favicon.ico" ||
			strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/vite") {
			return
		}
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

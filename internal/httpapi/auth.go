package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/Ciryanna/Veyra/internal/store"
)

type ctxKey int

const vkeyCtxKey ctxKey = 1

// CurrentVKey 取认证中间件注入的 vkey。未认证路由返回零值。
func CurrentVKey(ctx context.Context) store.VKey {
	v, _ := ctx.Value(vkeyCtxKey).(store.VKey)
	return v
}

// vkeyAuth 解析客户端凭证，按优先级取第一个非空：
//
//	Authorization: Bearer（OpenAI 生态 SDK）
//	x-api-key（Anthropic 生态 SDK，如 Claude Code）
//	x-goog-api-key（Gemini 生态 SDK）
//
// 三种头都指向同一个 vkey 空间——客户端用哪个 SDK 就发哪个头，
// 网关不应强迫改 SDK 的默认行为。
// 401 响应用 OpenAI 风格 error 对象，客户端 SDK 能直接展示 message。
func (s *Server) vkeyAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
		if key == "" {
			key = strings.TrimSpace(r.Header.Get("x-api-key"))
		}
		if key == "" {
			key = strings.TrimSpace(r.Header.Get("x-goog-api-key"))
		}
		if key == "" {
			s.writeAPIError(w, http.StatusUnauthorized, "missing_api_key",
				"缺少凭证（Authorization: Bearer / x-api-key / x-goog-api-key）")
			return
		}
		v, err := s.st.FindVKeyByHash(r.Context(), store.HashVKey(key))
		if err != nil {
			// 不存在与禁用同错，避免向探测者泄露应用清单。
			s.writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", "虚拟 key 无效或已禁用")
			return
		}
		// last_used_at 是统计字段，更新失败不影响转发（store 层注释同款约定）。
		go func(id int64) {
			ctx, cancel := context.WithTimeout(context.Background(), 5e9)
			defer cancel()
			_ = s.st.TouchLastUsed(ctx, id)
		}(v.ID)
		next(w, r.WithContext(context.WithValue(r.Context(), vkeyCtxKey, v)))
	}
}

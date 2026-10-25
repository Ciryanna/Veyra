package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// webFS 是内嵌的前端产物（web/ 构建输出）。
// 仓库里的 webdist/ 只含占位页：clone 后直接 go build 可运行（无 UI），
// `cd web && npm run build` 后重新编译即得完整控制台——单二进制交付。
//
//go:embed webdist
var webFS embed.FS

func (s *Server) webRoutes(mux *http.ServeMux) {
	sub, err := fs.Sub(webFS, "webdist")
	if err != nil {
		return // embed 静态目录，编译期已保证存在
	}
	fileServer := http.FileServerFS(sub)
	// "/" 是 ServeMux 兜底模式：/v1/*、/admin/api/*、/healthz 更具体，优先命中。
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(sub, p); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}

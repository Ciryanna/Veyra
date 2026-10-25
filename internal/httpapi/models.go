package httpapi

import (
	"net/http"
)

// handleModels 输出 OpenAI 格式的模型列表，id 即路由名 "provider/model"。
// 数据来自 models 登记册（enabled provider 的 enabled 模型）；
// 未登记的动态模型不出现，但调用不受影响（见 handleChat 注释）。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.st.ListModels(r.Context())
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "list_models", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id":       m.ProviderName + "/" + m.Name,
			"object":   "model",
			"owned_by": m.ProviderName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

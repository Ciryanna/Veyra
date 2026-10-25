// Package config 汇集全部启动配置。
//
// 刻意只走环境变量（参考 ciryanna-account 的形态）：单二进制 + 一个 SQLite 文件
// + env。providers/vkeys/model 等业务数据一律进数据库由 CLI/UI 管理，
// 配置文件不承载业务状态，避免"改了文件不生效/重启被回滚"两类事故。
package config

import (
	"fmt"
	"os"
	"strings"
)

// Role 是本节点在双节点级联中的身份：
//
//	standalone — 全直连（M1/M2 行为，默认）。单机部署用这个。
//	primary    — 国内主节点：region=overseas 的 provider 自动改道经
//	             Tailscale 转发到 egress，其余直连。
//	egress     — 国外出口节点：就是一个标准 Veyra（行为与 standalone 相同），
//	             为 primary 签发一个节点 vkey 即可；显式命名只为日志/UI 可辨识。
type Role string

const (
	RoleStandalone Role = "standalone"
	RolePrimary    Role = "primary"
	RoleEgress     Role = "egress"
)

type Config struct {
	Env        string // "dev" | "prod"
	ListenAddr string // HTTP 监听地址
	DBPath     string // SQLite 文件路径
	AdminToken string // 管理端令牌；空 = 不注册管理路由
	Role       Role

	// EgressURL/EgressKey 仅 primary 模式使用：
	// egress 节点的 Tailscale 地址（如 http://100.64.0.2:8080，不含 /v1）
	// 与 egress 上签发的节点 vkey。真实上游凭证只存在 egress 的库里——
	// primary 上 overseas provider 的 base_url/api_key 是占位符，不参与转发。
	EgressURL string
	EgressKey string
}

// LoadFromEnv 读取 VEYRA_* 环境变量。校验失败即启动失败——
// "带错误配置也能跑"会把问题推迟到最难排查的运行期。
func LoadFromEnv() (Config, error) {
	cfg := Config{
		Env:        envOr("VEYRA_ENV", "prod"),
		ListenAddr: envOr("VEYRA_LISTEN", ":8080"),
		DBPath:     envOr("VEYRA_DB", "veyra.db"),
		AdminToken: strings.TrimSpace(os.Getenv("VEYRA_ADMIN_TOKEN")),
		Role:       Role(envOr("VEYRA_ROLE", string(RoleStandalone))),
		EgressURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("VEYRA_EGRESS_URL")), "/"),
		EgressKey:  strings.TrimSpace(os.Getenv("VEYRA_EGRESS_KEY")),
	}
	if cfg.Env != "dev" && cfg.Env != "prod" {
		return Config{}, fmt.Errorf("VEYRA_ENV 只接受 dev|prod，当前 %q", cfg.Env)
	}
	if cfg.DBPath == "" {
		return Config{}, fmt.Errorf("VEYRA_DB 不能为空")
	}
	switch cfg.Role {
	case RoleStandalone, RolePrimary, RoleEgress:
	default:
		return Config{}, fmt.Errorf("VEYRA_ROLE 只接受 standalone|primary|egress，当前 %q", cfg.Role)
	}
	// primary 必须知道往哪转：缺 egress 配置的 primary 会在运行期把
	// overseas 请求 502 掉，启动期就拒绝。
	if cfg.Role == RolePrimary && (cfg.EgressURL == "" || cfg.EgressKey == "") {
		return Config{}, fmt.Errorf("VEYRA_ROLE=primary 需要 VEYRA_EGRESS_URL 与 VEYRA_EGRESS_KEY")
	}
	// 非 primary 配了 egress 参数：大概率是复制粘贴出错，显式忽略会掩盖它。
	if cfg.Role != RolePrimary && (cfg.EgressURL != "" || cfg.EgressKey != "") {
		return Config{}, fmt.Errorf("VEYRA_EGRESS_URL/VEYRA_EGRESS_KEY 只在 VEYRA_ROLE=primary 时有效")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

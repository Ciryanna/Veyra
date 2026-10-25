# Veyra ↔ Bifrost 上游对照表

目的：Bifrost 上游新增/修复 provider 时，能以最小代价把成果带进 Veyra。
对照基于 2026-10 的上游主干（`maximhq/bifrost`，Apache 2.0）。

## 包级对应

| Veyra | Bifrost | 说明 |
|---|---|---|
| `internal/provider/` | `core/providers/` | 上游适配。Veyra 每协议一个文件（compat/anthropic/responses/gemini/systemone），Bifrost 每厂商一个包 |
| `internal/provider.Client.Do/DoWithHeaders` | 各 provider 包内的 HTTP 调用 | Veyra 把 HTTP 客户端抽成单例；Bifrost 用 fasthttp |
| `internal/httpapi/` | `transports/bifrost-http/` | HTTP 层。Veyra 标准库 ServeMux；Bifrost 有完整路由/中间件栈 |
| `internal/store/` | （无对应） | Bifrost 无持久层（配置文件 + 内存）；Veyra 用 SQLite 承载全部业务数据 |
| `internal/httpapi/webRoutes` + `web/` | `framework/`（React 面板） | 两边都是 React + go:embed；Veyra 自写精简控制台 |
| `cmd/veyra` | `cmd/` + `cli/` + `npx/` | Bifrost 有三种分发形态；Veyra 单二进制 |

## provider kind ↔ Bifrost provider 包

Veyra 的 kind 是"协议形态"，Bifrost 的包是"厂商"；一个 kind 能接任意厂商的
OpenAI 兼容端点，这是 Veyra 不需要 30+ provider 包的原因。

| Veyra kind | 对应 Bifrost 包（port 时看这些） |
|---|---|
| `openai-compat` | `openai`、`deepseek`、`openrouter`、`groq`、`mistral`、`xai`、`fireworks`…（几乎全部：它们本质都是 OpenAI 兼容） |
| `anthropic` | `anthropic`（认证头 `x-api-key` + `anthropic-version`；SSE 事件流） |
| `gemini` | `gemini`（URL 形态 `models/{model}:{method}`；认证头 `x-goog-api-key`；`usageMetadata`） |
| `systemone` | `typesafe`（Jev 决策模型，`POST /v1/systemone`，Bifrost 有现成的 decision.go/types.go 可参照） |

## port 一个新 provider 的步骤

Bifrost 上游出了新厂商（比如新的 OpenAI 兼容服务）：

1. **看它是不是 OpenAI 兼容**——九成是。是的话 Veyra 零代码：
   `veyra provider add --name <name> --base-url <url> --api-key <key>` 完事。
2. 不是兼容端点（新协议形态）时才写代码：
   - 在 Bifrost `core/providers/<name>/` 里找请求/响应结构、认证头、SSE 事件格式、usage 字段；
   - Veyra 侧新增一个 kind（CHECK 约束 + CLI/admin 校验 + UI 下拉）；
   - `internal/provider/<name>.go`：URL 构造 + 非流式 usage 提取 + SSE extractor（字段级合并语义，见 compat.go 注释）；
   - `internal/httpapi/<name>.go`：端点处理器（参照 responses.go 的最简形态：解析→路由→改道判断→DoWithHeaders→透传/流式→记账）；
   - 测试参照 protocols_test.go 的假上游模式。
3. 上游修了协议 bug（字段改名、usage 位置变化）：对应改 Veyra 的提取函数——两边提取逻辑结构相同，逐行对照即可。

## 刻意不 port 的能力

failover/负载均衡（单 BYOK 无需）、语义缓存、MCP 网关、agent 网关、
多租户/预算/组织、key 轮转（`keyselectors`）、billing 头、prompt 缓存
分发（`promptcache`）——这些是 Bifrost 的企业面，与"自用统一转发"的
定位冲突，砍掉即是目的，不是欠账。

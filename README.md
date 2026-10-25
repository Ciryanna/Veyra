# Veyra

自用 BYOK LLM 网关：统一管理你的各家 API Key，一个入口服务你的全部应用。

- **透传优先**：网关不改写协议体（OpenAI chat 只动 `model` 与 `stream_options`；Gemini 连 body 都不动），没有转换损耗，上游错误原样透传。
- **BYOK 专用**：全部上游凭证都是 API Key，没有订阅、没有分发，只给你的应用用——每个应用签发一个虚拟 Key（vkey），按应用看用量与成本。
- **单二进制**：Go + `modernc.org/sqlite`（纯静态、无 CGO）+ `go:embed` 内嵌 Web 控制台。一个可执行文件 + 一个 SQLite 文件就是全部部署物。
- **双节点级联**：国内主节点 + 国外出口节点（Tailscale 组网），国外 API 的真实凭证只存在出口节点。

## 快速开始

```bash
# 构建（首次需构建前端；之后只重跑 go build 即可）
cd web && npm install && npm run build && cd ..
go build -o veyra ./cmd/veyra

# 登记上游与模型（CLI 即配置面；--api-key 也可用环境变量 VEYRA_UPSTREAM_KEY 传入避免进 shell 历史）
./veyra provider add --name deepseek --base-url https://api.deepseek.com/v1 --api-key sk-xxx
./veyra model add --provider deepseek --name deepseek-chat --price-in 0.27 --price-out 1.10

# 为应用签发虚拟 key（明文只显示这一次）
./veyra vkey add --app qa-bot

# 启动
./veyra serve                          # 默认 :8080，UI 在 http://localhost:8080
```

调用（OpenAI SDK 把 `base_url` 指向网关即可）：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer vk-..." \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"你好"}]}'
```

模型名写作 `provider/model`：第一个 `/` 之前命中登记的 provider 名，其余全部是模型名（OpenRouter 式多级名天然兼容）。未登记的模型名也会透传——登记只为列表、改名映射与成本核算。

## 端点矩阵

| 端点 | 协议 | 认证头（填 vkey） | 上游认证 |
|---|---|---|---|
| `POST /v1/chat/completions` | OpenAI Chat（SSE + tool calls） | `Authorization: Bearer` | Bearer |
| `POST /v1/responses` | OpenAI Responses | Bearer | Bearer |
| `POST /v1/messages` | Anthropic Messages（Claude Code 直连） | `x-api-key` | x-api-key + anthropic-version |
| `POST /v1/systemone` | TypeSafe Jev（System One 决策模型） | Bearer | Bearer |
| `POST /v1beta/models/{provider/}model:method` | Gemini 原生（含 `?alt=sse` 流式与模型列表） | `x-goog-api-key` | x-goog-api-key |
| `GET /v1/models` | OpenAI 模型列表 | Bearer | — |
| `/admin/api/*` + `/` | 管理 API + Web 控制台 | `Bearer $VEYRA_ADMIN_TOKEN` | — |

三种凭证头指向同一个 vkey 空间：客户端用哪个生态的 SDK 就发哪个头。provider `kind` 与端点形态强绑定（`anthropic` 走 `/v1/messages`、`gemini` 走 `/v1beta/models/*`），不匹配会在网关层被早拒绝而不是拿到上游的诡异错误。

## CLI 参考

```
veyra serve                                启动网关
veyra provider add --name N --kind K --base-url U --api-key K [--region R]
veyra provider list|enable|disable|remove
veyra model add --provider P --name N [--upstream U] [--price-in F] [--price-out F]
veyra model list|remove
veyra vkey add --app A
veyra vkey list|enable|disable|remove
veyra usage [--by vkey|provider] [--since 24h|7d|30d]
```

- `--kind`：`openai-compat`（默认）｜`anthropic`｜`gemini`｜`systemone`
- `--base-url`：与 OpenAI SDK 的 `base_url` 一致（通常含 `/v1`）；anthropic 填 `https://api.anthropic.com/v1`；gemini 填 `https://generativelanguage.googleapis.com/v1beta`
- `--price-in/--price-out`：USD / 百万 token（如 `0.27`）。**非零即更新**，0/缺省保留原价
- 同名 `vkey add` = 重置凭证，旧 key 立即失效

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `VEYRA_LISTEN` | `:8080` | 监听地址 |
| `VEYRA_DB` | `veyra.db` | SQLite 文件路径 |
| `VEYRA_ENV` | `prod` | `dev` 输出文本日志 + debug 级别 |
| `VEYRA_ADMIN_TOKEN` | 空 | 管理端令牌；**空 = 不注册管理路由**（纯转发面） |
| `VEYRA_ROLE` | `standalone` | `primary`｜`egress`｜`standalone`，见下节 |
| `VEYRA_EGRESS_URL` / `VEYRA_EGRESS_KEY` | 空 | primary 节点的出口地址与节点 vkey |

配置只走环境变量；providers/models/vkeys 等业务数据一律进 SQLite（CLI/UI 管理）。

## 双节点级联（国内外）

```
应用 ──国内──> primary ──Tailscale──> egress ──直连──> 国外上游
                │
                └──直连──> 国内上游（region=domestic）
```

两台机器跑**同一个二进制**，仅环境变量不同：

```bash
# 国外出口节点（就是一个标准 Veyra）
VEYRA_ROLE=egress ./veyra serve

# 国外侧：登记真实凭证（只存在这台机器的库里）
veyra provider add --name openai --base-url https://api.openai.com/v1 --api-key sk-xxx --region overseas
veyra provider add --name typesafe --kind systemone --base-url https://api.typesafe.ai/v1 --api-key ts-xxx --region overseas
veyra vkey add --app primary-node        # 给主节点的 vkey

# 国内主节点
VEYRA_ROLE=primary \
VEYRA_EGRESS_URL=http://<egress的Tailscale地址>:8080 \
VEYRA_EGRESS_KEY=<节点vkey> \
./veyra serve

# 国内侧：同名 provider 只登记占位（region 必须也是 overseas）
veyra provider add --name openai --base-url http://placeholder.invalid/v1 --api-key placeholder --region overseas
```

改道规则：`region=overseas` 的 provider 在 primary 上自动改道经 egress 出国（所有协议端点都生效）；`domestic` 直连。模型互路由：应用只管请求 `openai/gpt-5` 或 `deepseek/deepseek-chat`，走国内还是出国由 provider 的 region 决定。成本在配置了价格的那一跳记录——egress 中转不重复计费，价格表配在你想看账的那台机器上即可。

## 成本核算

- 价格单位为 **USD / 百万 token**（CLI/UI 的人类单位），存储为整数 nanoUSD/token（`$0.27/Mtok` → `270`），一次整数乘加即得成本，无浮点累计误差。
- **记账时锁定**：每次调用的成本按当时的价格表记入 `usage_events`，之后改价不改历史。
- 未定价模型成本记 0（显示 `—`）——与 token 缺失同理，宁可 0 让配置问题可见，不做估算。
- Jev 只收输入费：`--price-in 0.042`，输出价留 0。

## 架构

```
cmd/veyra/          main + CLI 子命令（装配与生命周期，无业务规则）
internal/
  config/           环境变量加载与校验（启动失败优于运行期带病）
  store/            SQLite 访问层（WAL + _txlock=immediate + embed migrations）
  provider/         上游转发（URL 构造、认证头、SSE 逐行透传、usage 提取）
  httpapi/          路由 + 认证 + 透传 + 记账 + 管理 API + embed UI
web/                React 控制台（Vite + Tailwind，产物进 internal/httpapi/webdist）
```

设计要点（详见各包注释）：

- **透传不改写**：OpenAI chat 仅重写 `model`、注入 `stream_options.include_usage`（上游不认识时自动剥掉重发一次）；Gemini 的 model 在 URL 路径里，body 零改动。
- **usage 字段级合并**：一套 `StreamSSE` 兼容四种协议的流式 usage 出现模式（OpenAI 独立 chunk / Anthropic start+delta 分置 / Responses completed 一次给全 / Gemini 累计值）。
- **错误透传**：上游已应答的错误（含 4xx/5xx）原样回传客户端，网关不吞不重写——排查不能断在网关这层。成败都记账。
- **凭证边界**：上游真实 key 只存 egress 节点的库；vkey 只存 SHA-256 哈希；UI/列表永远只回脱敏值。

## 与 Bifrost 的关系

Veyra 是 Bifrost（maximhq/bifrost，Apache 2.0）的极简重写：砍掉 failover/负载均衡/语义缓存/MCP/agent 网关/多租户，只保留"BYOK 统一管理 + 透传 + 记账"这一条主线。provider 层的概念划分与上游对齐，新增 provider 时按上游同名包 port——对照表见 [docs/upstream-mapping.md](docs/upstream-mapping.md)。

## 测试

```bash
go test ./...        # store/provider/httpapi 三层，含双节点级联链路
cd web && npm run build   # 前端类型检查 + 产物
```

## License

[Apache-2.0](LICENSE)。上游 [Bifrost](https://github.com/maximhq/bifrost)（Apache-2.0）的极简重写，对照关系见 [docs/upstream-mapping.md](docs/upstream-mapping.md)。

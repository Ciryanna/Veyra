-- Veyra 初始 schema：providers / models / vkeys / usage_events。
--
-- 设计取舍：
--  * providers.api_key 存明文。自用 BYOK 网关，SQLite 文件本身在私网机器上，
--    权限边界是文件系统（与 LiteLLM/Bifrost 的做法一致）。
--    后续如需加固，走 AES-GCM + VEYRA_CRYPTO_KEY 派生密钥的迁移，不动表结构。
--  * 时间戳一律 Unix 毫秒，与 ciryanna-account 保持一致。
--  * models 表是"登记册"而非"白名单"：请求的模型没登记也允许透传
--    （提供什么用什么），登记只为 /v1/models 列表与显式 upstream 映射。

CREATE TABLE providers (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,              -- 路由前缀：deepseek / openai / typesafe ...
    kind        TEXT NOT NULL CHECK (kind IN ('openai-compat', 'systemone', 'anthropic', 'gemini')),
    base_url    TEXT NOT NULL,                     -- 不含尾斜杠
    api_key     TEXT NOT NULL,
    region      TEXT NOT NULL DEFAULT 'domestic' CHECK (region IN ('domestic', 'overseas')),
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE models (
    id             INTEGER PRIMARY KEY,
    provider_id    INTEGER NOT NULL REFERENCES providers(id),
    name           TEXT NOT NULL,                  -- 对外模型名（不含 provider 前缀）
    upstream_model TEXT NOT NULL,                  -- 发给上游的 model 值，默认等于 name
    -- 价格单位：nanoUSD / token（= USD per Mtok × 1000）。
    -- $3/Mtok → 3000；$0.042/Mtok → 42。整数乘加即可得成本，无浮点累计误差。
    -- 0 = 未定价：成本记 0，与 token 缺失同理——宁可 0 让配置问题可见，不估算。
    price_in_nano  INTEGER NOT NULL DEFAULT 0,     -- 每输入 token 的 nanoUSD
    price_out_nano INTEGER NOT NULL DEFAULT 0,     -- 每输出 token 的 nanoUSD
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     INTEGER NOT NULL,
    UNIQUE (provider_id, name)
);

CREATE TABLE vkeys (
    id           INTEGER PRIMARY KEY,
    app_name     TEXT NOT NULL UNIQUE,             -- 应用名：qa-bot / writer ...
    key_hash     TEXT NOT NULL UNIQUE,             -- SHA-256(key) hex；key 本体只在生成时打印一次
    key_prefix   TEXT NOT NULL,                    -- 显示用前缀（vk- 前 12 位），UI 定位不泄密
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);

CREATE TABLE usage_events (
    id            INTEGER PRIMARY KEY,
    vkey_id       INTEGER NOT NULL REFERENCES vkeys(id),
    provider_id   INTEGER REFERENCES providers(id), -- 上游网络失败时可为 NULL
    model         TEXT NOT NULL,                    -- 对外模型名（含 provider 前缀）
    endpoint      TEXT NOT NULL,                    -- 'chat' | 'systemone'
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    status_code   INTEGER NOT NULL DEFAULT 0,       -- 上游 HTTP 码；网络错误记 0
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    -- 记账时锁定的成本（nanoUSD，1e-9 USD）。价格取自当时的 models 表；
    -- 之后改价不影响历史——"当时花的钱"才是账实相符。
    cost_nano     INTEGER NOT NULL DEFAULT 0,
    error_msg     TEXT,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_usage_vkey_time ON usage_events(vkey_id, created_at);
CREATE INDEX idx_usage_provider_time ON usage_events(provider_id, created_at);
CREATE INDEX idx_models_provider ON models(provider_id);

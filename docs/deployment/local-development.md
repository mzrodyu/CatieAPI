# 本地开发

## 环境要求

- Node.js 24+
- npm 11+
- Go 1.26+

## 安装依赖

```bash
npm install
go mod tidy
```

## 启动开发环境

```bash
npm run dev
```

服务地址：

- Web 控制台：http://localhost:5173
- Go API 服务：http://localhost:8787

Vite 会把 `/api` 和 `/v1` 请求代理到本地 API 服务。

当前 API 服务使用 Go + Gin，入口位于 `cmd/catieapi/main.go`。

## 环境变量

可以复制 `.env.example` 作为本地配置参考。生产部署时不需要编辑 `.env` 文件，直接在 1Panel、Docker 或运行环境自带的环境变量配置界面填写即可。

```text
PORT=8787
STATIC_DIR=dist
CORS_ORIGIN=http://localhost:5173
ADMIN_TOKEN=
SECRET_KEY=
SESSION_TTL_HOURS=168
REQUEST_LIMIT_PER_MINUTE=60
PERSISTENCE=file
DATA_FILE=data/state.json
DATABASE_URL=postgres://catieapi:catieapi@localhost:5432/catieapi?sslmode=disable
DATABASE_MAX_OPEN_CONNS=10
DATABASE_MAX_IDLE_CONNS=5
DATABASE_CONN_MAX_LIFETIME_MINUTES=30
PROVIDER_MODE=mock
UPSTREAM_API_KEY=
UPSTREAM_TIMEOUT_SECONDS=60
AUTH_SUCCESS_URL=http://localhost:5173/
DISCORD_CLIENT_ID=
DISCORD_CLIENT_SECRET=
DISCORD_REDIRECT_URI=http://localhost:8787/api/auth/discord/callback
DISCORD_ALLOWED_GUILD_ID=
DISCORD_ALLOWED_ROLE_ID=
```

## 可用接口

### 健康检查

```bash
curl http://localhost:8787/api/health
```

### 运行配置

```bash
curl http://localhost:8787/api/config/status
```

### 用户列表

```bash
curl http://localhost:8787/api/users
```

如果设置了 `ADMIN_TOKEN`：

```bash
curl http://localhost:8787/api/users \
  -H "Authorization: Bearer <admin_token>"
```

### 用户详情

```bash
curl http://localhost:8787/api/users/usr_1001
```

### 创建 API Key

```bash
curl http://localhost:8787/api/users/usr_1002/api-keys \
  -H "Content-Type: application/json" \
  -d '{"name":"Local Test Key"}'
```

### 更新渠道状态

```bash
curl http://localhost:8787/api/channels/chn_1002 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"status":"standby"}'
```

### 更新渠道上游地址

```bash
curl http://localhost:8787/api/channels/chn_1002 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"baseUrl":"https://provider.example/v1"}'
```

### 更新渠道上游 Key

```bash
curl http://localhost:8787/api/channels/chn_1002 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"upstreamApiKey":"sk-provider-key"}'
```

响应只会返回 `upstreamKeySet`，不会返回明文上游 Key。

### 更新模型状态

```bash
curl http://localhost:8787/api/models/deepseek-v4 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"recommended":true}'
```

### 更新模型价格

```bash
curl http://localhost:8787/api/models/deepseek-v4 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"inputPricePer1K":0.002,"outputPricePer1K":0.004}'
```

### Chat Completions

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6","messages":[{"role":"user","content":"hello"}]}'
```

### 幂等请求

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Idempotency-Key: demo-001" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","messages":[{"role":"user","content":"hello"}]}'
```

同一个 `Idempotency-Key` 会返回第一次请求的缓存结果，避免重复扣费。

## 当前实现说明

当前默认使用文件持久化，数据写入 `data/state.json`。如果设置 `PERSISTENCE=memory`，重启服务后修改会丢失。

也可以使用 Postgres：

```text
PERSISTENCE=postgres
DATABASE_URL=postgres://catieapi:catieapi@localhost:5432/catieapi?sslmode=disable
```

详细说明见 [Postgres 持久化](postgres.md)。

API Key 创建后只返回一次完整 secret，后端保存 SHA-256 哈希。管理接口只返回 Key 前缀，不返回哈希。

管理接口会校验用户、Key、渠道和模型状态，避免写入未知状态值。渠道级 `upstreamApiKey` 优先于全局 `UPSTREAM_API_KEY`。

如果设置 `SECRET_KEY`，渠道级 `upstreamApiKey` 会以 AES-GCM 加密后写入 `data/state.json`。未设置时保持本地开发兼容，按明文存储；已经存在的明文 Key 仍可继续读取。

Chat Completions 会优先使用上游响应的 `usage.prompt_tokens` 和 `usage.completion_tokens`，按模型的 `inputPricePer1K` / `outputPricePer1K` 扣费并写入额度流水。流式请求当前使用本地估算。

Discord 登录配置见 [Discord 登录](discord-login.md)。配置 `DISCORD_ALLOWED_GUILD_ID` 可以限制服务器成员，配置 `DISCORD_ALLOWED_ROLE_ID` 可以进一步限制身份组。

后续需要完善：

- Postgres 规范化表结构
- 真实用户认证
- 密码哈希
- 更完整的供应商错误映射

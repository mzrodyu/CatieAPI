# Chat Completions

CatieAPI 第一版提供 OpenAI compatible 的 `/v1/chat/completions` 接口。默认使用 mock provider，用于验证网关认证、模型解析、渠道选择、日志记录和额度扣减流程。设置 `PROVIDER_MODE=compatible` 后，请求会转发到渠道配置的 OpenAI-compatible 上游。

## Request

```text
POST /v1/chat/completions
```

也可以省略 `/v1`：

```text
POST /chat/completions
```

## Headers

```text
Authorization: Bearer <catieapi_key>
Content-Type: application/json
Idempotency-Key: <optional_unique_key>
```

## Body

```json
{
  "model": "gpt-5.5",
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": "hello"
    }
  ]
}
```

`model` 支持稳定模型 ID，也支持后台配置的别名，例如 `gpt`、`安全区`、`f5`、`哈基米`、`ds`。

## Example

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_你的_api_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}'
```

省略 `/v1` 的写法：

```bash
curl http://localhost:8787/chat/completions \
  -H "Authorization: Bearer cat_你的_api_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","messages":[{"role":"user","content":"hello"}]}'
```

## Stream Example

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_你的_api_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"f5","stream":true,"messages":[{"role":"user","content":"hello"}]}'
```

流式响应使用 Server-Sent Events，并以 `data: [DONE]` 结束。

当前版本只对非流式响应缓存幂等结果。

## Models

```bash
curl http://localhost:8787/v1/models \
  -H "Authorization: Bearer cat_你的_api_key"
```

返回 OpenAI compatible 的模型列表：

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-5.5",
      "object": "model",
      "created": 1780000000,
      "owned_by": "openai"
    }
  ]
}
```

## Response

```json
{
  "id": "chatcmpl_1780000000000",
  "object": "chat.completion",
  "created": 1780000000,
  "model": "gpt-5.5",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "CatieAPI mock response via OpenAI Compatible. Provider adapters can forward this request to an OpenAI-compatible upstream."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 18,
    "total_tokens": 30
  }
}
```

## Errors

### Invalid API Key

```json
{
  "error": {
    "message": "Invalid CatieAPI key",
    "type": "invalid_request_error",
    "code": "invalid_api_key"
  }
}
```

### Insufficient Quota

```json
{
  "error": {
    "message": "Insufficient quota",
    "type": "billing_error",
    "code": "insufficient_quota"
  }
}
```

### Model Not Available

```json
{
  "error": {
    "message": "No available model: unknown-model",
    "type": "invalid_request_error",
    "param": "model",
    "code": "model_not_available"
  }
}
```

## Provider Mode

默认本地模式：

```text
PROVIDER_MODE=mock
```

转发到 OpenAI-compatible 上游：

```text
PROVIDER_MODE=compatible
UPSTREAM_API_KEY=<upstream_provider_key>
UPSTREAM_TIMEOUT_SECONDS=600
```

也可以给单个渠道设置 `upstreamApiKey`。渠道级 Key 优先于全局 `UPSTREAM_API_KEY`，管理 API 响应只返回 `upstreamKeySet`，不会返回明文 Key。`UPSTREAM_TIMEOUT_SECONDS` 可按部署环境改成更大的秒数。

建议生产环境设置：

```text
SECRET_KEY=<long_random_secret>
```

设置后，渠道级 `upstreamApiKey` 会以 AES-GCM 加密后持久化。未设置时保持本地开发兼容，按明文存储。

转发地址来自渠道的 `baseUrl`，例如渠道 `baseUrl` 为 `https://provider.example/v1` 时，请求会发送到：

```text
https://provider.example/v1/chat/completions
```

用户请求里的模型别名会先解析为 CatieAPI 的稳定模型 ID，再转发给上游。`stream: true` 会以 Server-Sent Events 透传上游响应。

## Billing

非流式请求会优先读取响应中的：

```json
{
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 50
  }
}
```

然后按模型的 `inputPricePer1K` 和 `outputPricePer1K` 扣减用户余额，并写入额度流水。流式请求当前使用本地 token 估算，后续可以在上游提供最终 usage 时改为精确结算。

## 当前限制

- 当前默认使用文件持久化，数据写入 `data/state.json`。
- 生产发布前需要接入数据库和更完整的供应商错误映射。

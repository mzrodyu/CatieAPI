# Models

CatieAPI 提供 OpenAI compatible 的模型列表接口。

## List Models

```text
GET /v1/models
```

也可以省略 `/v1`：

```text
GET /models
```

示例：

```bash
curl http://localhost:8787/v1/models \
  -H "Authorization: Bearer cat_live_test"
```

响应：

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-5.6",
      "object": "model",
      "created": 1780000000,
      "owned_by": "openai"
    }
  ]
}
```

## Retrieve Model

```text
GET /v1/models/{model}
```

也可以省略 `/v1`：

```text
GET /models/{model}
```

示例：

```bash
curl http://localhost:8787/v1/models/gpt-5.6 \
  -H "Authorization: Bearer cat_live_test"
```

`model` 支持模型 ID 和别名：

- `gpt-5.6`、`gpt-5.5`、`gpt`、`安全区`
- `claude-fable-5`、`f5`、`肥波5`
- `gemini-3.1`、`哈基米`、`基米`
- `deepseek-v4`、`ds`、`deepseek`、`鲸鱼`

## Pricing

管理接口中的模型对象包含：

```json
{
  "inputPricePer1K": 0.002,
  "outputPricePer1K": 0.004
}
```

价格单位是每 1K tokens 的余额扣费。`/v1/chat/completions` 会优先读取上游响应中的 `usage.prompt_tokens` 和 `usage.completion_tokens` 计算成本；流式请求暂时使用本地估算。

更新模型价格：

```bash
curl http://localhost:8787/api/models/deepseek-v4 \
  -X PATCH \
  -H "Content-Type: application/json" \
  -d '{"inputPricePer1K":0.002,"outputPricePer1K":0.004}'
```

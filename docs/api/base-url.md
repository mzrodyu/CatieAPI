# Base URL

CatieAPI 的核心体验是少填一段路径。

传统 OpenAI-compatible 网关通常要求：

```text
https://shiliyuming.com/v1
```

CatieAPI 可以直接填写：

```text
https://shiliyuming.com
```

后端会自动兼容这些路径：

```text
POST /chat/completions
POST /v1/chat/completions

GET /models
GET /v1/models

GET /models/{model}
GET /v1/models/{model}
```

## JavaScript

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "cat_你的_api_key",
  baseURL: "https://shiliyuming.com"
});

const response = await client.chat.completions.create({
  model: "ds",
  messages: [{ role: "user", content: "hello" }]
});

console.log(response.choices[0]?.message?.content);
```

## Python

```py
from openai import OpenAI

client = OpenAI(
    api_key="cat_你的_api_key",
    base_url="https://shiliyuming.com",
)

response = client.chat.completions.create(
    model="ds",
    messages=[{"role": "user", "content": "hello"}],
)

print(response.choices[0].message.content)
```

## curl

```bash
curl https://shiliyuming.com/chat/completions \
  -H "Authorization: Bearer cat_你的_api_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","messages":[{"role":"user","content":"hello"}]}'
```

仍然兼容标准 `/v1` 写法，方便迁移旧客户端。

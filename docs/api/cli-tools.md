# 一键接入各大 CLI 工具

CatieAPI 同时兼容两种主流协议，所以常见的 AI 命令行工具都能直接接入：

- OpenAI 风格：`/v1/chat/completions`、`/v1/responses`（Codex CLI、Aider、Cline、opencode 等）
- Anthropic Messages：`/v1/messages`（Claude Code）

只需要两样东西：

- Base URL：`https://shiliyuming.com`（省略 `/v1` 也可以）
- API Key：`cat_你的_api_key`

模型名填 CatieAPI 里的模型 ID 或别名，例如 `ds`、`gpt-5.5`。

## 一键脚本

仓库自带脚本，帮你把环境变量和 Codex 配置一次配好。

Windows PowerShell：

```powershell
./scripts/connect-cli.ps1 -BaseUrl "https://shiliyuming.com" -ApiKey "cat_你的_api_key" -Model "ds"
```

macOS / Linux：

```bash
BASE_URL="https://shiliyuming.com" API_KEY="cat_你的_api_key" MODEL="ds" \
  bash scripts/connect-cli.sh
```

脚本会设置 `ANTHROPIC_*` 和 `OPENAI_*` 环境变量，并在缺省时生成 Codex 配置。执行后重开终端即可生效。下面是每个工具的手动配置说明。

## Claude Code

Claude Code 走 Anthropic Messages 协议，对应 CatieAPI 的 `/v1/messages`。

设置环境变量：

```bash
export ANTHROPIC_BASE_URL="https://shiliyuming.com"
export ANTHROPIC_AUTH_TOKEN="cat_你的_api_key"
export ANTHROPIC_MODEL="ds"
export ANTHROPIC_SMALL_FAST_MODEL="ds"
```

然后正常启动：

```bash
claude
```

说明：

- `ANTHROPIC_AUTH_TOKEN` 会作为 `Authorization: Bearer` 发送，CatieAPI 也接受 `x-api-key`。
- Claude Code 会把请求发到 `ANTHROPIC_BASE_URL` + `/v1/messages`。
- 模型名要填 CatieAPI 里存在的模型，否则返回 `not_found_error`。

## Codex CLI

Codex CLI 走 OpenAI 协议。编辑 `~/.codex/config.toml`：

```toml
model = "gpt-5.5"
model_provider = "catieapi"

[model_providers.catieapi]
name = "CatieAPI"
base_url = "https://shiliyuming.com/v1"
env_key = "CATIEAPI_KEY"
wire_api = "chat"
```

设置 Key 并运行：

```bash
export CATIEAPI_KEY="cat_你的_api_key"
codex
```

说明：

- `wire_api = "chat"` 用 `/v1/chat/completions`，对各类上游模型兼容性最好。
- 也可以设 `wire_api = "responses"`，对应 CatieAPI 的 `/v1/responses`。

## Aider

Aider 基于 OpenAI 协议：

```bash
export OPENAI_API_BASE="https://shiliyuming.com"
export OPENAI_API_KEY="cat_你的_api_key"
aider --model openai/ds
```

模型名前缀 `openai/` 让 Aider 走 OpenAI-compatible 通道。

## Cline / Roo Code / Kilo Code

这些 VS Code 插件都提供 “OpenAI Compatible” 供应商，填写：

- API Provider：`OpenAI Compatible`
- Base URL：`https://shiliyuming.com/v1`
- API Key：`cat_你的_api_key`
- Model ID：`ds`（或其它 CatieAPI 模型）

## opencode

在 `~/.config/opencode/opencode.json` 里加一个 OpenAI-compatible 供应商：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "catieapi": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "CatieAPI",
      "options": {
        "baseURL": "https://shiliyuming.com/v1",
        "apiKey": "cat_你的_api_key"
      },
      "models": {
        "ds": { "name": "CatieAPI ds" }
      }
    }
  }
}
```

启动后选择 `catieapi/ds`。

## 通用 OpenAI SDK

任何用 OpenAI SDK 的工具都能接入：

```bash
export OPENAI_BASE_URL="https://shiliyuming.com"
export OPENAI_API_KEY="cat_你的_api_key"
```

```py
from openai import OpenAI

client = OpenAI()  # 读取上面的环境变量
response = client.chat.completions.create(
    model="ds",
    messages=[{"role": "user", "content": "hello"}],
)
print(response.choices[0].message.content)
```

## Gemini CLI

Gemini CLI 使用 Google 自有协议，CatieAPI 暂不提供该协议的入站兼容，因此不能直接接入。需要 Gemini 系模型时，可在 CatieAPI 配置对应渠道，再用上面任意一个 OpenAI 协议工具调用。

## 排查

- `not_found_error` / `model_not_available`：模型名不在 CatieAPI 里，检查模型 ID 或别名。
- `authentication_error` / `invalid_api_key`：Key 不对或没带上，确认环境变量已生效（重开终端）。
- `permission_error` / `model_not_allowed`：这个 Key 被限制了可用模型范围。
- 想确认服务连通，先用 curl 测 `/v1/messages` 或 `/v1/chat/completions`。

验证 Anthropic 入口：

```bash
curl https://shiliyuming.com/v1/messages \
  -H "x-api-key: cat_你的_api_key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}'
```

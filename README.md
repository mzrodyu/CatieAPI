# CatieAPI

CatieAPI 是一个轻量、清爽、面向大众用户的 AI 聚合网关。它兼容 OpenAI 风格接口，但不要求用户记住复杂路径：Base URL 可以直接填写 `https://shiliyuming.com`，不用写 `https://shiliyuming.com/v1`。

目标不是复刻 NewAPI 的复杂度，而是提供一个更轻、更清楚、更像 iOS 原生应用体验的模型网关。

## 为什么做它

- 少配置：用户 Base URL 直接填域名即可。
- 少心智负担：模型、渠道、Key、额度和日志围绕日常使用组织。
- 少部署成本：一个 Go 服务托管 API 和前端，适合 1Panel / Docker。
- 少视觉噪音：不做重型后台大屏，不堆渐变和复杂装饰。

## 快速体验

```bash
curl http://localhost:8787/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","messages":[{"role":"user","content":"hello"}]}'
```

也兼容标准路径：

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6","messages":[{"role":"user","content":"hello"}]}'
```

## SDK 示例

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "cat_live_test",
  baseURL: "https://shiliyuming.com"
});

const response = await client.chat.completions.create({
  model: "ds",
  messages: [{ role: "user", content: "hello" }]
});
```

## License

MIT License. Copyright (c) 2026 Catie.

## 产品方向

- 极简：默认只展示用户当前需要完成的操作。
- 清爽：界面使用 iOS 风格的留白、分组、毛玻璃感和清晰层级，但不使用渐变色。
- 轻量：核心路径围绕模型接入、密钥管理、额度计费、调用转发和日志排障。
- 可维护：后端 API、前端视图、文档、部署方案从第一天就保持清楚边界。
- 大众化：降低普通用户接入模型 API 的门槛，同时保留高级用户需要的兼容 OpenAI API 能力。

## 核心模块

- 网关转发：兼容 OpenAI 风格接口，统一转发到不同模型供应商。
- 渠道管理：配置供应商、模型、权重、优先级、故障切换。
- 用户管理：注册、登录、用户状态、角色权限、额度、封禁、备注。
- API Key 管理：创建、禁用、删除、权限范围、调用统计。
- 计费与额度：套餐、余额、消耗记录、充值记录。
- 日志与审计：请求日志、错误日志、渠道健康状态。
- 管理后台：用户、渠道、模型、价格、系统配置。
- 文档中心：快速开始、API 参考、部署指南、常见问题。

## 设计原则

- 不做重型后台大屏。
- 不堆叠卡片和装饰元素。
- 不使用渐变色作为主视觉。
- 图标统一使用 SVG。
- 表单、列表、设置页采用 iOS 设置页式分组。
- 支持浅色和暗色模式，界面使用系统分组、材质、分段控件和开关。

## 文档入口

- [Base URL 与 SDK 示例](docs/api/base-url.md)
- [项目工作流](docs/WORKFLOW.md)
- [文档方案](docs/DOCUMENTATION_PLAN.md)
- [本地开发](docs/deployment/local-development.md)
- [1Panel 部署](docs/deployment/1panel.md)
- [Discord 登录](docs/deployment/discord-login.md)
- [Postgres 持久化](docs/deployment/postgres.md)
- [路线图](docs/ROADMAP.md)
- [参与贡献](CONTRIBUTING.md)

## 本地启动

```bash
npm install
go mod tidy
npm run dev
```

默认地址：

- Web 控制台：http://localhost:5173
- Go API 服务：http://localhost:8787

## 已实现 MVP 骨架

- React 管理后台
- Go + Gin API 后端
- Go 生产环境托管前端 `dist`
- Docker / 1Panel 部署配置
- iOS 风格浅色/暗色界面
- 分段控件、系统开关、玻璃材质导航、分组列表
- 移动端底部 Tab、移动列表布局、横向滑动快捷操作
- Live Gateway 面板、请求流转视图、快捷动作入口
- SVG 图标
- 用户管理
- Discord OAuth 登录
- 服务器 ID / 身份组 ID 准入
- API Key 展示、创建、启停
- 渠道管理
- OpenAI-compatible 上游转发
- compatible 模式 SSE 流式透传
- 渠道启停、模型启停
- 渠道级上游 Key 配置
- 调用日志
- 文件持久化：`data/state.json`
- Postgres 持久化：`PERSISTENCE=postgres`
- OpenAI compatible `/v1/chat/completions`

测试网关：

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6","messages":[{"role":"user","content":"hello"}]}'
```

也可以省略 `/v1`：

```bash
curl http://localhost:8787/chat/completions \
  -H "Authorization: Bearer cat_live_test" \
  -H "Content-Type: application/json" \
  -d '{"model":"ds","messages":[{"role":"user","content":"hello"}]}'
```

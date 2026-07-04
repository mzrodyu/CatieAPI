# CatieAPI 文档方案

## 1. 文档目标

CatieAPI 的文档要服务三类人：

- 普通用户：知道怎么创建 API Key、查看余额、复制调用示例。
- 开发者：知道怎么调用兼容 OpenAI 的接口。
- 部署者：知道怎么本地运行、配置环境变量、上线部署和排障。

文档风格应保持直接、短句、可执行。每篇文档都应该有明确目的，不写空泛介绍。

## 2. 文档结构

建议文档目录：

```text
docs/
  README.md
  quick-start.md
  concepts.md
  api/
    authentication.md
    chat-completions.md
    models.md
    errors.md
  user-guide/
    api-keys.md
    usage-and-billing.md
    logs.md
  admin-guide/
    users.md
    channels.md
    models-and-pricing.md
    quota.md
    system-settings.md
  deployment/
    local-development.md
    environment-variables.md
    docker.md
    production.md
    backup-and-restore.md
  design/
    product-principles.md
    ui-style-guide.md
    svg-icons.md
  development/
    architecture.md
    gateway-flow.md
    adapter-design.md
    database-schema.md
    testing.md
  troubleshooting/
    request-failed.md
    quota-not-deducted.md
    stream-response.md
    upstream-errors.md
```

## 3. 必写文档

### 快速开始

位置：`docs/quick-start.md`

内容：

- 注册或登录
- 创建 API Key
- 选择模型
- 复制调用示例
- 查看调用日志

验收标准：

- 用户跟着文档可以完成第一条请求。
- 示例命令可以直接复制执行。

### API 认证

位置：`docs/api/authentication.md`

内容：

- `Authorization: Bearer <CatieAPI Key>`
- API Key 权限
- Key 泄露后的处理方式
- 常见认证错误

### Chat Completions

位置：`docs/api/chat-completions.md`

内容：

- 请求地址
- 请求头
- 请求体
- 普通响应
- 流式响应
- 错误响应
- 与 OpenAI 兼容差异

### 渠道管理

位置：`docs/admin-guide/channels.md`

内容：

- 什么是渠道
- 如何添加供应商
- 如何配置模型映射
- 权重、优先级、启停状态
- 健康检查
- 故障切换逻辑

### 用户管理

位置：`docs/admin-guide/users.md`

内容：

- 用户列表
- 用户搜索和筛选
- 用户详情
- 用户状态
- 角色权限
- 额度调整
- 用户 API Key
- 用户调用日志
- 封禁和解封
- 管理员备注

### 部署指南

位置：`docs/deployment/production.md`

内容：

- 环境变量
- 数据库
- 反向代理
- HTTPS
- 日志
- 备份
- 升级流程

## 4. API 文档规范

每个接口文档固定包含：

- 用途
- 请求方法和路径
- 权限要求
- 请求头
- 请求参数
- 响应字段
- 错误码
- curl 示例
- JavaScript 示例
- 注意事项

示例结构：

```md
# Create Chat Completion

## Request

POST /v1/chat/completions

## Headers

Authorization: Bearer <api_key>
Content-Type: application/json

## Body

...

## Response

...

## Errors

...
```

## 5. 设计文档规范

### 产品原则

位置：`docs/design/product-principles.md`

需要明确：

- 为什么 CatieAPI 要比 NewAPI 更轻
- 哪些功能暂时不做
- 普通用户和高级用户的取舍
- 后台页面为什么不做大屏风格

### UI 风格指南

位置：`docs/design/ui-style-guide.md`

需要明确：

- 色彩
- 字体
- 间距
- 圆角
- SVG 图标规范
- 表格规范
- 设置页规范
- 空状态和错误状态

### SVG 图标规范

位置：`docs/design/svg-icons.md`

需要明确：

- 图标统一为 SVG
- 默认线宽
- 尺寸规格
- 命名规则
- 禁止混用位图小图标
- 禁止使用复杂插画代替功能图标

## 6. 开发文档规范

### 架构文档

位置：`docs/development/architecture.md`

内容：

- 前端架构
- 后端架构
- 数据库
- 缓存
- 队列
- 日志
- 部署拓扑

### 网关流程

位置：`docs/development/gateway-flow.md`

内容：

- API Key 校验
- 用户状态校验
- 额度校验
- 渠道选择
- 请求转换
- 响应转换
- 日志记录
- 额度扣减

### 适配器设计

位置：`docs/development/adapter-design.md`

内容：

- 供应商适配器接口
- 模型映射
- 错误码归一
- 流式响应处理
- 重试策略

## 7. 文档更新规则

以下变更必须同步更新文档：

- 新增接口
- 修改请求或响应字段
- 新增环境变量
- 修改部署流程
- 修改计费逻辑
- 修改权限逻辑
- 修改 UI 主路径
- 新增供应商适配器

PR 或提交检查项：

- 代码是否改变用户可见行为
- 是否需要更新快速开始
- 是否需要更新 API 文档
- 是否需要更新部署文档
- 是否需要更新排障文档

## 8. 第一批文档任务

优先级 P0：

- `docs/quick-start.md`
- `docs/deployment/local-development.md`
- `docs/deployment/environment-variables.md`
- `docs/api/authentication.md`
- `docs/api/chat-completions.md`
- `docs/admin-guide/users.md`
- `docs/admin-guide/channels.md`

优先级 P1：

- `docs/development/architecture.md`
- `docs/development/gateway-flow.md`
- `docs/design/ui-style-guide.md`
- `docs/troubleshooting/request-failed.md`

优先级 P2：

- `docs/api/models.md`
- `docs/user-guide/usage-and-billing.md`
- `docs/admin-guide/models-and-pricing.md`
- `docs/deployment/production.md`

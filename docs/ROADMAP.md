# Roadmap

CatieAPI 的路线是轻量优先，不做复杂重后台。

## 已完成

- Go + Gin 后端
- React 管理后台
- OpenAI-compatible Chat Completions
- 可省略 `/v1` 的 Base URL
- Models 接口与模型别名
- API Key 哈希存储
- 渠道级上游 Key
- 渠道 Key AES-GCM 加密持久化
- SSE 流式透传
- usage 计费与额度流水
- Discord OAuth 登录
- Discord 服务器 ID / 身份组 ID 准入
- Postgres 持久化
- Docker / 1Panel 部署

## 下一阶段

- Postgres 规范化表结构
- 渠道健康检查
- 加权路由与失败重试
- 上游错误映射
- 管理操作审计日志
- 前端登录页与权限态
- 用户自助面板
- 充值/套餐接口
- 更完整的供应商适配器

## 暂不做

- 重型后台大屏
- 复杂多租户企业套件
- 花哨主题市场
- 把所有供应商配置都堆进一个页面

## 设计取舍

CatieAPI 会优先保持：

- 少配置
- 易部署
- 易理解
- 移动端可用
- OpenAI-compatible 生态兼容

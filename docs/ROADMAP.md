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
- 渠道级输入/输出 Token 价格
- 渠道健康检查与模型拉取
- Discord OAuth 登录
- Discord 服务器 ID / 身份组 ID 准入
- 前端初始化、登录和用户自助面板
- 日志搜索、筛选、详情与服务端分页
- Postgres 持久化
- Docker / 1Panel 部署

## 下一阶段

- 加权路由与失败重试
- 日志保留策略与自动清理
- 配置和数据备份、恢复
- 上游错误映射
- 管理操作审计日志
- 额度流水查询与导出
- API Key 模型权限、过期时间和独立限流
- OpenAI 兼容接口回归测试矩阵
- Postgres 规范化表结构
- 更完整的供应商适配器

## 暂不做

- 支付网关、在线充值、套餐和返利系统
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

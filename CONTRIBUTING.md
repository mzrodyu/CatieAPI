# Contributing

感谢关注 CatieAPI。

这个项目更适合做小而清楚的改动。欢迎提交：

- OpenAI-compatible 兼容性修复
- 供应商适配器
- 部署文档
- UI 细节优化
- 移动端体验修复
- 后端测试
- 错误映射和日志增强

## 开发

```bash
npm install
go mod tidy
npm run dev
```

测试：

```bash
go test ./...
npm run build
```

## 设计约束

- 不使用渐变作为主视觉
- 不做重型后台大屏
- 图标使用 SVG
- 保持 iOS 风格的分组、留白和清晰层级
- 后端优先保持 OpenAI-compatible
- Base URL 应继续支持不带 `/v1`

## 提交建议

- 一个 PR 只解决一个问题
- 新后端能力尽量补测试
- 文档和环境变量变更要同步
- 不提交本地 `data/`、`dist/`、日志和截图产物

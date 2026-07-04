# 1Panel 部署

CatieAPI 推荐在 1Panel 的容器或运行环境里配置环境变量，不需要进入服务器手动编辑 `.env` 文件。仓库里的 `.env.example` 只是变量清单。

## 容器方式

镜像会同时包含 Go API 和前端 `dist`，一个端口即可运行完整应用。

端口：

```text
8787
```

必须配置：

```text
PORT=8787
STATIC_DIR=/app/dist
PERSISTENCE=postgres
DATABASE_URL=postgres://<user>:<password>@<host>:5432/<db>?sslmode=disable
ADMIN_TOKEN=<long_admin_token>
SECRET_KEY=<long_random_secret>
CORS_ORIGIN=https://your-domain.example
AUTH_SUCCESS_URL=https://your-domain.example/
```

如果使用 Discord 登录：

```text
DISCORD_CLIENT_ID=<discord_oauth_client_id>
DISCORD_CLIENT_SECRET=<discord_oauth_client_secret>
DISCORD_REDIRECT_URI=https://your-domain.example/api/auth/discord/callback
DISCORD_ALLOWED_GUILD_ID=<server_or_guild_id>
DISCORD_ALLOWED_ROLE_ID=<role_id>
SESSION_TTL_HOURS=168
```

如果接真实上游：

```text
PROVIDER_MODE=compatible
UPSTREAM_API_KEY=<fallback_provider_key>
UPSTREAM_TIMEOUT_SECONDS=60
```

渠道级上游 Key 仍建议在后台渠道管理里配置；全局 `UPSTREAM_API_KEY` 只作为兜底。

## 反向代理

如果 1Panel 反向代理到容器端口 `8787`，同一个域名即可访问：

```text
https://your-domain.example/
https://your-domain.example/api/health
https://your-domain.example/v1/chat/completions
```

Discord OAuth 的 Redirect URI 必须和公网域名完全一致：

```text
https://your-domain.example/api/auth/discord/callback
```

## 环境变量原则

- 不需要在生产服务器编辑 `.env`
- 1Panel 的“环境变量”区域就是最终配置来源
- `.env.example` 只用于查看变量名和默认值
- `SECRET_KEY` 上线后不要随意更换，否则已加密的渠道上游 Key 无法解密
- `ADMIN_TOKEN` 和 `DISCORD_CLIENT_SECRET` 不要写进前端代码

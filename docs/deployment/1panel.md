# 1Panel 部署

CatieAPI 推荐在 1Panel 的容器或运行环境里配置环境变量，不需要进入服务器手动编辑 `.env` 文件。仓库里的 `.env.example` 只是变量清单。

## 容器方式

镜像会同时包含 Go API 和前端 `dist`，一个端口即可运行完整应用。

镜像：

```text
ghcr.io/mzrodyu/catieapi:latest
```

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

变量说明：

| 变量 | 作用 |
| --- | --- |
| `PORT` | CatieAPI 容器内监听端口，默认 `8787`。 |
| `STATIC_DIR` | 前端静态文件目录，容器镜像内固定用 `/app/dist`。 |
| `PERSISTENCE` | 持久化方式。生产建议 `postgres`。 |
| `DATABASE_URL` | Postgres 连接地址。注意容器里不要写 `localhost`，要写 1Panel 提供的数据库主机或服务名。 |
| `ADMIN_TOKEN` | 管理接口备用令牌。没有 Discord session 时，可用 `Authorization: Bearer <ADMIN_TOKEN>` 访问后台接口。 |
| `SECRET_KEY` | 用来加密保存渠道上游 Key。上线后不要随意更换。 |
| `CORS_ORIGIN` | 前端访问域名，例如 `https://api.example.com` 或你的站点域名。 |
| `AUTH_SUCCESS_URL` | Discord 登录成功后跳转的前端地址。 |

如果使用 Discord 登录：

```text
DISCORD_CLIENT_ID=<discord_oauth_client_id>
DISCORD_CLIENT_SECRET=<discord_oauth_client_secret>
DISCORD_REDIRECT_URI=https://your-domain.example/api/auth/discord/callback
DISCORD_ALLOWED_GUILD_ID=<server_or_guild_id>
DISCORD_ALLOWED_ROLE_ID=<role_id>
SESSION_TTL_HOURS=168
```

Discord 变量说明：

| 变量 | 作用 |
| --- | --- |
| `DISCORD_CLIENT_ID` | Discord Developer Portal 里的 OAuth2 Client ID。 |
| `DISCORD_CLIENT_SECRET` | Discord OAuth2 Client Secret。不要公开。 |
| `DISCORD_REDIRECT_URI` | Discord 回调地址，必须和 Developer Portal 中配置的 Redirect 完全一致。 |
| `DISCORD_ALLOWED_GUILD_ID` | 允许登录的 Discord 服务器 ID。为空则不限制服务器。 |
| `DISCORD_ALLOWED_ROLE_ID` | 允许登录的身份组 ID。设置它时必须同时设置服务器 ID。 |
| `SESSION_TTL_HOURS` | 登录 session 有效时间，单位小时。 |

如果接真实上游：

```text
PROVIDER_MODE=compatible
UPSTREAM_API_KEY=<fallback_provider_key>
UPSTREAM_TIMEOUT_SECONDS=60
```

上游变量说明：

| 变量 | 作用 |
| --- | --- |
| `PROVIDER_MODE` | 上游模式。`mock` 为本地模拟，`compatible` 为转发到 OpenAI-compatible 上游。 |
| `UPSTREAM_API_KEY` | 全局兜底上游 Key。更推荐在后台渠道里配置渠道级 Key。 |
| `UPSTREAM_TIMEOUT_SECONDS` | 请求上游的超时时间，单位秒。 |

渠道级上游 Key 仍建议在后台渠道管理里配置；全局 `UPSTREAM_API_KEY` 只作为兜底。

## GHCR 镜像

推送到 `main` 后，GitHub Actions 会自动构建并发布镜像：

```text
ghcr.io/mzrodyu/catieapi:latest
ghcr.io/mzrodyu/catieapi:sha-<commit>
```

如果 1Panel 拉取失败，检查 GitHub 仓库的 Package 可见性。公开项目建议把 package 设置为 Public，这样 1Panel 不需要额外登录 GHCR。

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

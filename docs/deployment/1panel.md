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
DATABASE_URL=postgres://数据库用户名:数据库密码@数据库地址:5432/数据库名?sslmode=disable
SECRET_KEY=你的随机加密密钥
CORS_ORIGIN=*
```

变量说明：

| 变量 | 作用 |
| --- | --- |
| `PORT` | CatieAPI 容器内监听端口，默认 `8787`。 |
| `STATIC_DIR` | 前端静态文件目录，容器镜像内固定用 `/app/dist`。 |
| `PERSISTENCE` | 持久化方式。生产建议 `postgres`。 |
| `DATABASE_URL` | Postgres 连接地址。注意容器里不要写 `localhost`，要写 1Panel 提供的数据库主机或服务名。 |
| `SECRET_KEY` | 用来加密保存渠道上游 Key。上线后不要随意更换。 |
| `CORS_ORIGIN` | 允许浏览器跨域调用 API 的来源。公开网关可用 `*`；限制来源时用逗号分隔，例如 `https://app.example.com,https://bot.example.com`。不要填写 API 自己的域名，除非调用页面也运行在该域名。 |

首次打开站点会进入初始化页面。直接创建管理员账号和密码，登录后即可在“设置”中配置 Discord、开放注册等选项。

`ADMIN_TOKEN` 不再是正常使用的必填项。如需保留管理 API 应急入口，可额外设置：

```text
ADMIN_TOKEN=你的应急管理密钥
```

Discord 配置会加密保存到当前持久化存储，保存后立即生效，不需要重启容器。以下环境变量仅作为首次启动或故障恢复时的可选兜底：

```text
DISCORD_CLIENT_ID=你的Discord应用Client ID
DISCORD_CLIENT_SECRET=你的真实client_secret
DISCORD_REDIRECT_URI=https://你的域名/api/auth/discord/callback
DISCORD_ALLOWED_GUILD_ID=你的服务器ID
DISCORD_ALLOWED_ROLE_ID=你的身份组ID
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
UPSTREAM_API_KEY=你的上游供应商Key
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
ghcr.io/mzrodyu/catieapi:sha-提交哈希
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
- 数据库连接和加密密钥由 1Panel 环境变量提供
- 管理员账号、密码和日常设置在 CatieAPI 页面中管理
- Discord 登录等日常配置优先在 CatieAPI 后台管理
- `.env.example` 只用于查看变量名和默认值
- `SECRET_KEY` 上线后不要随意更换，否则已加密的渠道上游 Key 无法解密
- 可选的 `ADMIN_TOKEN` 和 `DISCORD_CLIENT_SECRET` 不要提交到 Git 仓库

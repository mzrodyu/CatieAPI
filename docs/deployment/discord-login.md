# Discord 登录

CatieAPI 支持 Discord OAuth2 登录，并可用服务器 ID 和身份组 ID 控制管理后台准入。

## 环境变量

```text
ADMIN_TOKEN=你的管理密钥
SESSION_TTL_HOURS=168
AUTH_SUCCESS_URL=http://localhost:5173/
DISCORD_CLIENT_ID=1446547305208746115
DISCORD_CLIENT_SECRET=你的真实client_secret
DISCORD_REDIRECT_URI=http://localhost:8787/api/auth/discord/callback
DISCORD_ALLOWED_GUILD_ID=你的服务器ID
DISCORD_ALLOWED_ROLE_ID=你的身份组ID
```

`DISCORD_ALLOWED_GUILD_ID` 是 Discord 服务器 ID。`DISCORD_ALLOWED_ROLE_ID` 是身份组 ID。只设置服务器 ID 时，服务器成员都可以通过；同时设置身份组 ID 时，必须拥有该身份组。

如果设置了 `DISCORD_ALLOWED_ROLE_ID`，必须同时设置 `DISCORD_ALLOWED_GUILD_ID`。

## Discord Developer Portal

在 Discord 应用的 OAuth2 设置中加入 Redirect：

```text
http://localhost:8787/api/auth/discord/callback
```

生产环境改成自己的域名，例如：

```text
https://api.example.com/api/auth/discord/callback
```

## 登录流程

打开：

```text
http://localhost:8787/api/auth/discord/start
```

CatieAPI 会请求这些 scope：

```text
identify guilds.members.read
```

登录回调后，后端会读取：

```text
GET /users/@me
GET /users/@me/guilds/{guild.id}/member
```

通过校验后，后端写入 `catie_session` HttpOnly Cookie。管理接口在设置 `ADMIN_TOKEN` 时会接受两种凭证：

- `Authorization: Bearer 你的管理密钥`
- 通过 Discord 登录得到的 `catie_session`

## 退出

```bash
curl http://localhost:8787/api/auth/logout \
  -X POST
```

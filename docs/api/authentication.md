# API 认证

CatieAPI 使用 Bearer Token 认证。

## Header

```text
Authorization: Bearer <catieapi_key>
```

## 示例

```bash
curl http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer cat_你的_api_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6","messages":[{"role":"user","content":"hello"}]}'
```

## Key 展示规则

- 用户创建后只展示一次完整 Key。
- 后台只展示 Key 前缀。
- 后端保存 SHA-256 哈希，不保存明文。
- 管理 API 返回的 `apiKey` 对象不会包含哈希。
- 管理员可以禁用或删除异常 Key。

本地种子 Key：

```text
cat_你的管理_key
cat_你的_api_key
```

## 认证失败

认证失败返回：

```json
{
  "error": {
    "message": "Invalid CatieAPI key",
    "type": "invalid_request_error",
    "code": "invalid_api_key"
  }
}
```

## 管理接口

默认本地开发不启用管理接口鉴权。设置 `ADMIN_TOKEN` 后，除 `/api/health` 和 `/api/config/status` 外，其他 `/api/*` 管理接口都需要管理员令牌：

```bash
curl http://localhost:8787/api/users \
  -H "Authorization: Bearer <admin_token>"
```

也可以启用 Discord OAuth 登录。登录成功后，后端会写入 `catie_session` HttpOnly Cookie，管理接口会接受该 session。可通过 `DISCORD_ALLOWED_GUILD_ID` 限制服务器成员，通过 `DISCORD_ALLOWED_ROLE_ID` 限制身份组。

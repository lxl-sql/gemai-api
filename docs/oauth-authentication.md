# OAuth 鉴权接口文档

本文档描述本系统作为 OAuth 2.0 Authorization Server 时，外部应用在生产环境中的标准对接方式。

## 核心原则

- 外部应用只能获得 OAuth `access_token` 与授权范围内的用户资料或受限代理 API 能力。
- 本系统不会向外部应用返回 dashboard `session` cookie。
- `api` scope 不再下发用户的永久 API Key。外部应用如需代用户管理平台令牌，应申请 `api.token.manage` 受限 scope，并使用短期 OAuth token 调用专用委托接口。
- 外部应用需要使用自己的 Cookie/Session 维持登录态，不能复用本系统会话。

## 1. 创建 OAuth 应用

管理员在控制台创建 OAuth 应用后，会得到：

- `client_id`
- `client_secret`，仅创建或重置时显示一次
- 已注册的 `redirect_uri` 列表

`redirect_uri` 要求：

- 生产环境必须使用 `https://`
- 本地开发允许 `http://localhost`、`http://127.0.0.1`、`http://[::1]`
- 不允许 `fragment`，例如 `#token`
- 不允许 URL userinfo，例：`https://user:pass@example.com/callback`

## 2. 发起授权

外部应用将用户浏览器跳转到：

```http
GET /oauth/authorize
```

查询参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `response_type` | 是 | 固定为 `code` |
| `client_id` | 是 | OAuth 应用的 Client ID |
| `redirect_uri` | 是 | 必须与已注册回调地址完全一致 |
| `scope` | 否 | 空格分隔，默认 `profile` |
| `state` | 强烈建议 | 外部应用生成的 CSRF 随机值，回调时原样返回 |
| `code_challenge` | 建议 | PKCE S256 challenge |
| `code_challenge_method` | 使用 PKCE 时必填 | 当前仅支持 `S256` |

支持的 scope：

| Scope | 说明 |
| --- | --- |
| `profile` | 用户 ID、用户名、显示名 |
| `email` | 邮箱地址 |
| `api.token.manage` | 高风险权限，允许外部应用代用户查看、创建、修改、删除平台 API 令牌 |

示例：

```text
https://api.example.com/oauth/authorize?response_type=code&client_id=gai_xxx&redirect_uri=https%3A%2F%2Ftool.example.com%2Fauth%2Fcallback&scope=profile%20email&state=random_state&code_challenge=...&code_challenge_method=S256
```

## 3. 用户授权回调

用户同意后，浏览器会跳转到外部应用注册的 `redirect_uri`：

```text
https://tool.example.com/auth/callback?code=AUTH_CODE&state=random_state
```

外部应用必须：

1. 校验 `state` 与发起授权时保存的值一致。
2. 使用后端服务拿 `code` 换取 OAuth `access_token`。
3. 不要在前端直接暴露 `client_secret`。

用户拒绝授权时回调：

```text
https://tool.example.com/auth/callback?error=access_denied&error_description=user_denied&state=random_state
```

## 4. 授权码换 Token

```http
POST /api/oauth-server/token
Content-Type: application/x-www-form-urlencoded
```

请求参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `grant_type` | 是 | 固定为 `authorization_code` |
| `code` | 是 | 授权回调得到的授权码 |
| `client_id` | 是 | OAuth 应用 Client ID |
| `redirect_uri` | 是 | 必须与授权请求完全一致 |
| `client_secret` | 条件必填 | 未使用 PKCE 时必填 |
| `code_verifier` | 条件必填 | 使用 PKCE 时必填 |

也支持 HTTP Basic 客户端认证：

```http
Authorization: Basic base64(client_id:client_secret)
```

示例：

```bash
curl -X POST "https://api.example.com/api/oauth-server/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code" \
  -d "code=AUTH_CODE" \
  -d "client_id=gai_xxx" \
  -d "client_secret=CLIENT_SECRET" \
  -d "redirect_uri=https://tool.example.com/auth/callback"
```

PKCE 示例：

```bash
curl -X POST "https://api.example.com/api/oauth-server/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code" \
  -d "code=AUTH_CODE" \
  -d "client_id=gai_xxx" \
  -d "redirect_uri=https://tool.example.com/auth/callback" \
  -d "code_verifier=CODE_VERIFIER"
```

成功响应：

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "profile email api.token.manage"
}
```

说明：

- 授权码有效期为 10 分钟。
- 授权码只能使用一次。
- 响应不会包含 `session`。
- 响应不会包含用户永久 API Key。
- `access_token` 的 JWT payload 包含 `sub`、`client_id`、`grant_id`、`scope`、`exp`、`jti`、`typ=oauth_access_token`、`token_use=delegated_api`。

## 5. 获取用户信息

```http
GET /api/oauth-server/userinfo
Authorization: Bearer <access_token>
```

响应示例：

```json
{
  "sub": 123,
  "username": "alice",
  "display_name": "Alice",
  "email": "alice@example.com"
}
```

字段说明：

| 字段 | Scope | 说明 |
| --- | --- | --- |
| `sub` | 总是返回 | 本系统用户 ID，外部应用应使用它作为绑定主键 |
| `username` | `profile` | 用户名 |
| `display_name` | `profile` | 显示名 |
| `email` | `email` | 邮箱 |

`access_token` 过期、应用被禁用或删除时，`userinfo` 会返回 `401 invalid_token`。

## 6. 代用户管理平台 API 令牌

当用户授权了 `api.token.manage` 后，外部应用可以使用 OAuth `access_token` 调用专用委托接口。

这些接口不会接受 dashboard session，也不会使用用户永久 API Key：

```http
Authorization: Bearer <oauth_access_token>
```

可用接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/oauth-delegated/token/` | 分页获取用户令牌列表，返回脱敏 key |
| `GET` | `/api/oauth-delegated/token/search` | 搜索用户令牌 |
| `GET` | `/api/oauth-delegated/token/{id}` | 获取单个令牌信息，返回脱敏 key |
| `POST` | `/api/oauth-delegated/token/` | 创建令牌 |
| `PUT` | `/api/oauth-delegated/token/` | 修改令牌 |
| `DELETE` | `/api/oauth-delegated/token/{id}` | 删除令牌 |
| `POST` | `/api/oauth-delegated/token/{id}/key` | 查看单个令牌完整 key，高风险 |
| `POST` | `/api/oauth-delegated/token/batch` | 批量删除令牌 |
| `POST` | `/api/oauth-delegated/token/batch/keys` | 批量查看令牌完整 key，高风险 |

鉴权会校验：

- OAuth access token 签名有效且未过期
- `typ=oauth_access_token`
- `token_use=delegated_api`
- OAuth App 仍启用
- 用户仍启用
- 授权记录 `grant_id` 未撤销
- token 与授权记录都包含 `api.token.manage`

如果未授权 `api.token.manage`，接口返回：

```json
{
  "error": "insufficient_scope",
  "error_description": "required scope: api.token.manage"
}
```

## 7. 查询和撤销授权

用户可以查看并撤销自己授权给外部应用的记录。

```http
GET /api/oauth-server/grants
```

该接口需要 dashboard 登录态，返回当前用户授权记录。

撤销授权：

```http
DELETE /api/oauth-server/grants/{id}
```

撤销后：

- 对应 grant 下所有旧 OAuth access token 会立即不可用于 `/userinfo`
- 对应 grant 下所有旧 OAuth access token 会立即不可用于 `/api/oauth-delegated/*`
- 管理员禁用 OAuth 应用后，旧 token 也会立即不可用

## 8. 外部应用如何保持会话

外部应用完成 `/userinfo` 后，应创建自己的本地会话：

1. 使用 `sub` 查找或创建外部应用内的用户。
2. 在外部应用服务端创建 session。
3. 设置外部应用自己的 Cookie，例如 `tool_session=...`。
4. Cookie 建议设置 `HttpOnly`、`Secure`、`SameSite=Lax`。

后续外部应用请求只校验自己的 session，不需要复用本系统 Cookie。

## 9. 错误响应

Token 端点遵循 OAuth 风格错误：

```json
{
  "error": "invalid_grant",
  "error_description": "authorization code is invalid"
}
```

常见错误：

| error | 场景 |
| --- | --- |
| `invalid_request` | 缺少必填参数 |
| `unsupported_grant_type` | `grant_type` 不是 `authorization_code` |
| `invalid_client` | `client_id` 或 `client_secret` 无效 |
| `invalid_grant` | 授权码无效、过期、已使用、`redirect_uri` 不一致或 PKCE 校验失败 |
| `invalid_token` | `access_token` 无效、过期，或应用已被禁用/删除 |

## 10. 旧版行为迁移

历史版本可能返回过：

- token 响应里的 `session`
- `api` scope 下的用户永久 `access_token`

修复后这两类字段不再返回。外部应用需要改为：

- 使用 OAuth `access_token` 调 `/userinfo` 获取身份。
- 使用自己的 session 保持登录。
- 如确实需要代用户管理平台 API 令牌，应改用 `api.token.manage` 与 `/api/oauth-delegated/token/*`。

# 多实例前端部署与发布

`web/default` 和 `web/classic` 都在构建时嵌入 Go 二进制。它们是客户端渲染 SPA，不需要 Node.js SSR 服务，但滚动发布时必须避免 HTML 与内容哈希静态资源跨版本混用。

## 必需配置

### 固定会话密钥

所有实例必须配置相同且长期稳定的 `SESSION_SECRET`。如果未配置，每个进程会生成不同的随机值，负载均衡切换实例后会导致登录会话失效。

### 使用同一镜像摘要

同一批实例必须从相同镜像摘要启动，不要混用浮动标签对应的不同镜像：

```text
image: example/image@sha256:<digest>
```

发布完成后，可通过系统实例页面或实例上报信息确认所有节点的运行版本一致。

### Redis 可用性

API 限流仍依赖 Redis。前端内容哈希静态资源已绕过 Web 限流，因此 Redis 短暂故障不会再阻断入口 JS/CSS；Redis 本身仍应使用高可用部署和连接健康检查。

## 推荐发布方式

优先使用蓝绿发布：

1. 启动全部新版本实例，但暂不接收外部流量。
2. 检查 `/healthz`、首页 HTML 和一个 `/static/js/*` 资源。
3. 确认新实例版本一致。
4. 一次性把流量切到新实例。
5. 等待连接排空后停止旧实例。

如果必须滚动发布，推荐把前端静态资源放在共享 CDN 或对象存储，并保留至少前后两个版本的 hash 文件。仅使用粘性会话不能完全解决资源错配，因为会话尚未建立、健康检查切换或代理重试仍可能落到另一实例。

## 主题切换

运行时切换 default/classic 后，各实例同步配置存在时间窗口。应用会在当前主题找不到文件时尝试另一主题的同名 hash 文件，但运维侧仍应：

1. 在低流量窗口切换主题。
2. 等待至少一个完整配置同步周期。
3. 确认所有实例返回相同主题首页后再继续发布或扩缩容。

## 缓存与错误响应

预期响应策略：

- `/` 与 SPA 页面：`Cache-Control: no-cache`，并使用 ETag。
- `/static/*`、`/assets/*`：`Cache-Control: public, max-age=31536000, immutable`。
- 缺失的 `/static/*`、`/assets/*`：返回 404，不能返回 `index.html`。
- default 页面捕获 chunk/CSS 加载失败后只自动刷新一次，避免无限刷新。

反向代理或 CDN 不得覆盖这些策略，也不要把 SPA fallback HTML 缓存为静态资源响应。

## 发布验证

每次发布至少验证：

```text
GET /api/status
GET /healthz
GET /
GET /static/js/<index-hash>.js
GET /static/js/<不存在的文件>.js  -> 404
```

浏览器侧使用冷缓存检查：

- 首屏没有长时间空白。
- `/api/status` 只有一条应用数据请求链。
- `/api/setup` 不阻塞 React 首次挂载。
- 控制台没有 `ChunkLoadError`、脚本 MIME 错误或循环刷新。

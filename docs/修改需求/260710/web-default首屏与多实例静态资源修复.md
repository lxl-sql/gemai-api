# Web Default 首屏与多实例静态资源修复

> 日期：2026-07-10  
> 范围：`web/default` 首屏启动链路、Go 静态资源托管、多实例滚动发布、运行时主题切换与健康检查。  
> 目标：消除首屏阻塞和重复请求，降低初始包体，修复多实例下静态资源 500、HTML/chunk 错配及白屏问题。

## 1. 背景与结论

`web/default` 不是服务端渲染应用。Go 服务只通过 `go:embed` 提供构建后的 `index.html`、JS、CSS 和字体，浏览器再通过 `ReactDOM.createRoot` 完成客户端渲染。

原先看到的 500 或 `/500` 页面主要来自以下路径：

- API 请求返回 500 后，React Query 全局错误处理主动跳转 `/500`。
- Web 全局限流依赖 Redis，Redis 命令失败会让入口 JS/CSS 直接返回 HTTP 500。
- 多实例滚动发布期间，HTML 和 hash chunk 可能来自不同版本实例。
- Rsbuild 实际产物位于 `/static/*`，后端缓存与 SPA fallback 保护却只识别 `/assets/*`。缺失的 `/static/*.js` 可能被当成前端路由并返回 `index.html`，最终表现为脚本解析错误、CSS MIME 错误或白屏。
- default/classic 主题配置依赖实例周期同步，窗口期内不同实例可能服务不同主题的 HTML 和静态资源。

首屏体感慢的主要原因不是 SSR，而是：

- 根路由首次进入时等待 `/api/setup`。
- 默认首页等待 `/api/home_page_content` 后才渲染 Hero。
- `/api/status` 同时从入口脚本、系统配置 hook 和 React Query 多路径请求。
- 七套语言包同步打进入口依赖。
- 通知中心未打开时也会引入 Markdown、KaTeX 和 DOMPurify。
- 首页首屏以下区块、Lora variable font 和 Hero 入场动画共同推迟可见内容。

## 2. 后端静态资源修复

### 2.1 统一识别前端构建资源

新增：

- `common/web_assets.go`

`common.IsFrontendAssetPath` 同时识别：

- `/static/*`
- `/assets/*`

缓存、限流和 SPA fallback 共用同一判断，避免不同中间件对构建目录理解不一致。

### 2.2 修复静态资源缓存

涉及：

- `middleware/cache.go`

调整后：

- `/static/*`、`/assets/*` 返回 `Cache-Control: public, max-age=31536000, immutable`。
- HTML、SPA 路由和非 hash 根目录资源继续使用 `no-cache`。
- 判断基于 `Request.URL.Path`，不会被 query string 干扰。

### 2.3 缺失静态资源返回 404

涉及：

- `router/web-router.go`

缺失的 `/static/*`、`/assets/*` 不再进入 SPA HTML fallback，而是通过 not-found 响应返回 404 和 `Cache-Control: no-store`。

这可以避免浏览器把 HTML 当作 JS/CSS 解析，也避免代理或 CDN 缓存错误的静态资源响应。

### 2.4 静态资源绕过 Redis Web 限流

涉及：

- `middleware/rate-limit.go`

GET/HEAD 的内容 hash 静态资源不再经过 `GlobalWebRateLimit`。Redis 短暂不可用时，入口 JS/CSS 仍可正常下载，避免整站白屏。

API 和普通 Web 请求仍保留原有限流行为，不会降低业务接口的防护。

### 2.5 default/classic 静态文件交叉 fallback

涉及：

- `common/embed-file-system.go`

当前主题文件系统找不到请求文件时，会继续尝试另一主题文件系统。

该机制主要处理主题配置尚未在所有实例完成同步的窗口：用户从一个实例取得 default HTML 后，即使静态请求落到仍处于 classic 状态的实例，也可以按 hash 文件名找到 default 资源。

它不能解决新旧二进制版本之间完全不同的 hash chunk，因此生产发布仍推荐蓝绿切流或共享静态资源存储。

## 3. 多实例健康检查

### 3.1 新增 `/healthz`

涉及：

- `controller/misc.go`
- `router/api-router.go`

`/healthz` 不经过 API Redis 限流，用于负载均衡和容器 readiness 检查：

- 主数据库不可用时返回 503。
- 启用 Redis 且 Redis 不可用时返回 503。
- 依赖可用时返回 200、`success=true` 和当前运行版本。

### 3.2 Docker 健康检查

涉及：

- `docker-compose.yml`

健康检查从只验证进程存活的 `/api/status` 改为 `/healthz`，避免数据库或 Redis 已故障的实例继续被认为健康并接收流量。

## 4. 首屏启动链路修复

### 4.1 setup 检查不再阻塞 React 挂载

涉及：

- `web/default/src/routes/__root.tsx`

原先根路由 `beforeLoad` 会在首次访问时 await `/api/setup`，请求完成前根 DOM 保持为空。

现在改为：

- React 和路由先完成挂载。
- 根组件在 effect 中后台检查 setup 状态。
- 未初始化时使用客户端路由跳转 `/setup`。
- 已确认初始化的结果继续写入本地缓存，避免后续导航重复检查。

### 4.2 首页不再等待自定义内容接口

涉及：

- `web/default/src/features/home/hooks/use-home-page-content.ts`
- `web/default/src/features/home/index.tsx`
- `web/default/src/features/home/types.ts`

调整后：

- hook 初始化时同步读取 `localStorage` 中的自定义首页内容。
- 没有缓存时立即显示默认 Hero，不再展示全屏 `Loading...`。
- `/api/home_page_content` 在后台刷新。
- 服务端返回自定义 Markdown、HTML 或 URL 后再切换对应展示。

首次访问配置了自定义首页但尚无本地缓存时，可能短暂显示默认首页；这是为了避免把整站首屏锁在一个 API RTT 后。

### 4.3 `/api/status` 收敛为单一数据源

涉及：

- `web/default/src/main.tsx`
- `web/default/src/routes/__root.tsx`
- `web/default/src/hooks/use-status.ts`
- `web/default/src/hooks/use-system-config.ts`

现在：

- `main.tsx` 只使用本地缓存同步设置标题和 favicon，不再自行发起网络刷新。
- 根组件通过唯一的 React Query `['status']` 查询刷新状态。
- Header、Hero、导航、公告等消费者共享同一 query。
- 系统配置 hook 只读取 store 和预加载 logo，不再使用原生 `fetch` 再请求一次 `/api/status`。
- status 请求完成后统一更新系统配置 store、loading 状态和本地缓存。

## 5. 首屏包体与渲染优化

### 5.1 语言包按需加载

涉及：

- `web/default/src/i18n/config.ts`
- `web/default/src/components/language-switcher.tsx`
- `web/default/src/features/auth/hooks/use-auth-redirect.ts`
- `web/default/src/features/profile/components/language-preferences-card.tsx`

调整前，en、zh、zh-TW、fr、ru、ja、vi 七套完整 JSON 都静态进入入口依赖。

调整后：

- 英文作为基础 fallback。
- 启动时只加载当前检测语言。
- 用户切换语言或登录后恢复语言偏好时，先动态加载对应资源，再执行 `changeLanguage`。
- 语言选择继续写入 `i18nextLng`，刷新后保持一致。
- 语言 chunk 加载失败时回退英文，避免应用无法挂载。

### 5.2 通知中心异步分包

涉及：

- `web/default/src/components/layout/components/public-header.tsx`

`NotificationPopover` 改为 `React.lazy`。Markdown、KaTeX、DOMPurify、Tabs 和 Popover 等通知展示依赖不再同步阻塞入口 chunk 解析。

### 5.3 首页首屏以下内容延迟加载

涉及：

- `web/default/src/features/home/index.tsx`

首屏立即渲染 Hero；以下区块在浏览器空闲时再加载：

- Stats
- Features
- HowItWorks
- CTA
- Footer

使用 `requestIdleCallback` 并提供超时 fallback，避免低端设备长期不加载后续内容。

### 5.4 Lora 字体按需加载

涉及：

- `web/default/src/styles/index.css`
- `web/default/src/styles/lora.css`
- `web/default/src/context/theme-customization-provider.tsx`

Lora 不再全局同步进入首屏 CSS。只有用户选择 serif 字体或启用默认使用 serif 的主题预设时，才动态加载 Lora CSS 和字体文件。

Public Sans 仍作为默认字体保留。

### 5.5 CSS 和 Hero 可见时间

涉及：

- `web/default/rsbuild.config.ts`
- `web/default/src/features/home/components/sections/hero.tsx`

修改：

- 生产环境启用 Tailwind CSS optimize。
- Hero 主标题、描述和主要操作按钮不再从 opacity 0 延迟渐入。
- 右侧终端演示保留动画，但延迟从 320ms 缩短为 120ms。

## 6. 资源版本与加载失败自愈

### 6.1 default 资源加载失败恢复

涉及：

- `web/default/index.html`

新增：

- 构建版本 meta。
- 捕获同源 `/static/*`、`/assets/*` script/link 加载错误。
- 捕获 `ChunkLoadError`、CSS chunk 加载失败和动态 import 失败。
- 发生错误后附加 cache-bust 参数刷新一次。
- 使用 sessionStorage 限制重试，避免错误持续时无限刷新。

### 6.2 classic 兼容

涉及：

- `web/classic/index.html`
- `main.go`

修改：

- classic 原有资源错误恢复逻辑补充 `/static/*` 判断。
- `InjectBuildVersion` 同时注入 default 和 classic 的 index page；原先只处理 default，classic 中的版本占位符不会被替换。

## 7. 首屏构建结果

基于修复前后的生产构建产物，HTML 直接引用的初始 JS/CSS 资源：

- 修复前：约 5.32 MB raw，gzip 约 1.48 MB。
- 修复后：约 2.94 MB raw，gzip 约 756 KB。
- gzip 体积降低约 49%。
- 主入口 `index` chunk 从约 3.41 MB raw / 962 KB gzip，下降到约 1.04 MB raw / 241 KB gzip。

非英文环境会额外请求当前语言的一个异步 chunk，但不再下载其余六套语言。

全量 dist 仍包含管理后台、图表、编辑器、KaTeX、Shiki 等路由异步 chunk。全量 dist 大小不等于首屏下载量。

## 8. 多实例发布要求

### 8.1 必须统一的配置

- 所有实例使用相同且稳定的 `SESSION_SECRET`。
- 同一批实例使用相同镜像摘要，不要让同一浮动 tag 指向不同构建。
- Redis 应使用高可用部署；静态资源虽然已不依赖 Redis 限流，但 API 限流和其他缓存仍依赖 Redis。

### 8.2 推荐蓝绿发布

1. 启动全部新版本实例，但暂不接收外部流量。
2. 检查 `/healthz`、首页 HTML 和一个 `/static/js/*` 文件。
3. 确认新实例上报版本一致。
4. 一次性切换负载均衡流量。
5. 等待旧实例连接排空后停止旧版本。

如果必须滚动发布，推荐把前端静态资源放到共享 CDN 或对象存储，并至少保留前后两个版本的 hash 文件。粘性会话只能缓解，不能彻底解决静态资源跨版本问题。

### 8.3 缓存和代理要求

- `/` 与 SPA 页面保持 `no-cache` 并允许 ETag。
- `/static/*`、`/assets/*` 保持长期 immutable。
- 反向代理不得把缺失静态资源改写为 SPA HTML。
- CDN 不得缓存错误的 HTML/JSON 为 JS 或 CSS。

## 9. 测试与验证

新增测试：

- `common/web_assets_test.go`
  - 验证 `/static/*`、`/assets/*` 识别。
  - 验证 default/classic 文件系统交叉 fallback。
- `middleware/web_assets_test.go`
  - 验证静态资源 immutable 缓存。
  - 验证 Redis 不可用时静态资源绕过 Web 限流。
- `controller/health_test.go`
  - 验证数据库可用且未启用 Redis时 `/healthz` 返回成功和版本。

已执行：

- `go test ./...`
- `web/default`：`bun run typecheck`
- 修改文件：`oxlint`
- `web/default`：`bun run build`
- `web/classic`：`bun run build`
- `web/default`：`bun run i18n:sync`
- `docker compose config -q`
- `git diff --check`
- IDE linter 检查

以上检查均通过。`docker compose config -q` 仅提示仓库现有 `version` 字段已过时，不影响配置有效性。

## 10. 上线后观察项

- `/healthz` 503 次数及对应数据库、Redis 状态。
- `/static/*` 的 404、500 和响应 Content-Type。
- 浏览器 `ChunkLoadError`、CSS chunk error、脚本 MIME 错误及自动刷新次数。
- 首屏 `/api/status` 请求数量。
- FCP、LCP、初始 JS transfer size 和 JS parse/compile 时间。
- 系统实例上报版本是否一致。
- 主题切换后是否存在 default/classic 混合资源请求。

更完整的部署操作说明见：

- `docs/installation/multi-instance-frontend.md`

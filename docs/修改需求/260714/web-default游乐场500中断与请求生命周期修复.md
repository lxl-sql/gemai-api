# Web Default 游乐场 500 中断与请求生命周期修复

> 日期：2026-07-14；二次审计与补充修复：2026-07-15  
> 范围：`web/default` 全局 React Query 错误处理、游乐场流式与非流式请求生命周期，以及 `relay/channel` 上游请求取消传播  
> 目标：避免后台接口的单次 500 将用户强制带离回答页面，并在页面卸载、重新发送或主动停止时将取消信号完整传播到上游请求。

## 1. 问题现象

用户在游乐场等待模型回答时，页面偶尔会跳转到 `api.gemai.cc/500`。回答随即从界面中断，但账户仍可能产生消费。停留时间越长，后台查询或网络瞬态失败触发该现象的概率越高。

截图中的 `/500` 是前端 SPA 错误路由，不代表浏览器当前展示的是最初失败接口返回的原始 HTTP 500 页面。实际链路是：某个 React Query 查询先收到 500，随后全局错误处理主动导航到 `/500`。

## 2. 根因分析

### 2.1 任意查询 500 都会破坏当前页面

原 `web/default/src/main.tsx` 在全局 `QueryCache.onError` 中包含以下行为：

```ts
if (error.response?.status === 500) {
  toast.error(i18next.t('Internal Server Error!'))
  router.navigate({ to: '/500' })
}
```

这意味着 `/api/status`、`/api/notice`、`/api/user/models`、`/api/user/self/groups` 等页面依赖或后台查询中的任意一个返回 500，都会销毁当前游乐场页面。局部数据失败被错误地提升成了整页故障。

`docs/修改需求/260710/web-default首屏与多实例静态资源修复.md` 已记录过该风险，但当前源码仍保留全局跳转，本次对该链路完成实际修复。

### 2.2 生产环境重试放大等待和请求量

原查询策略使用 `failureCount > 3` 才停止重试。一次持续失败最多会形成初始请求加四次重试，延长失败暴露时间，也会给正在异常的接口增加额外压力。除 401、403 外，普通 400、404、500 等确定性错误也会被重复请求。

### 2.3 页面卸载没有关闭 SSE

原 `useStreamRequest` 没有 React 卸载清理逻辑。路由跳到 `/500` 后，游乐场 UI 已经消失，但 `SSE` 实例和事件回调仍可能继续存活，浏览器连接不一定立即结束。

因此会出现以下不一致：

1. 用户看不到后续回答；
2. 上游仍可能继续生成；
3. 后端根据已经生成或最终上报的用量结算；
4. 用户感知为“回答中断但仍扣费”。

同时，连续发起两次流式请求时，旧连接的延迟事件可能影响新请求状态；`error` 与 `readystatechange` 也可能对同一次失败重复回调。

### 2.4 非流式请求卸载时没有取消

非流式请求虽然使用了 `AbortController`，但组件卸载时只清理了流式刷新定时器，没有中止当前 HTTP 请求。切换页面后，请求仍可能继续占用连接并在后台完成。

### 2.5 局部错误提示与 Axios 全局提示重复

模型列表和用户分组查询已经在各自 Hook 中展示带业务语义的错误提示，但请求仍会经过 Axios 全局错误提示，单次失败可能出现两条 Toast。

二次审计还发现，普通 Mutation 的 Axios 错误会先经过 Axios 响应拦截器，再经过 React Query 全局 `MutationCache.onError`，同一错误同样会重复提示；304 分支也存在多处提示入口。

### 2.6 浏览器取消没有完整传播到上游

前端关闭 SSE 或中止普通 HTTP 请求，只能取消浏览器到网关的连接。原通用中继请求通过 `http.NewRequest` 创建，未继承 Gin 入站请求的 Context；非流式 padding 分支还从 `context.Background()` 派生 Context。客户端断开后，网关到模型供应商的请求仍可能继续等待或生成。

WebSocket 虽可使用 `DialContext` 取消 TCP 建连，但 gorilla/websocket 在 TCP 已连接、等待握手响应头时不会仅因 Context 取消而立即关闭底层连接，因此还需要将连接本身绑定到入站请求生命周期。

### 2.7 提前 EOF、取消重试与测试运行器冲突

- SSE 收到 HTTP 200 后，如果连接在 `[DONE]` 前关闭，原逻辑不会报错，`isStreaming` 可能一直保持为 `true`。
- Axios `CanceledError` 没有 HTTP 状态码，原重试判断会把用户主动取消当作网络故障继续重试。
- 3 个 `node:test` 文件会被 Vitest 收集后判定为 `No test suite found`。
- Base UI 日期时间组件测试在 Vitest 默认 `forks` 池中完成断言后无法正常退出子进程，导致全量测试挂起。

## 3. 已落地修复

### 3.1 收敛全局查询错误处理

新增：

- `web/default/src/lib/query-client.ts`
- `web/default/src/lib/query-client.test.ts`

调整：

- `web/default/src/main.tsx`

新的查询客户端策略如下：

- 删除 HTTP 500 自动跳转 `/500` 的逻辑；局部接口失败时保留当前页面和已经生成的回答。
- 401 仍统一清理登录状态并跳转登录页。
- 开发环境不自动重试，便于直接观察失败。
- 生产环境只重试 Axios 网络错误以及 502、503、504 瞬态网关错误。
- 每次查询最多额外重试两次。
- 400、401、403、404、500、Axios 取消错误和非 Axios 异常不再重试。
- 保留原有 `refetchOnWindowFocus: false` 和 10 秒 `staleTime` 行为。
- 非 Axios Mutation 异常仍由 React Query 通用错误处理承接；Axios HTTP 错误只由响应拦截器提示一次。
- 304 文案集中到 Axios 响应拦截器，避免重复 Toast。

重试判断被提取为纯函数 `shouldRetryQuery`，便于直接测试错误状态和重试边界。

### 3.2 完善 SSE 生命周期

调整：

- `web/default/src/features/playground/hooks/use-stream-request.ts`

新增：

- `web/default/src/features/playground/hooks/use-stream-request.test.tsx`

修复后的行为：

- 创建新流前先关闭旧流。
- 组件卸载时关闭当前 SSE 连接，并清空引用。
- 每个流使用独立的 `settled` 状态，完成或失败后只允许结算一次前端状态。
- 所有消息和错误事件都校验事件来源仍是当前活动连接。
- 旧流的延迟消息、延迟错误不会关闭或污染新流。
- `message`、`error`、`readystatechange` 和同步启动异常不会重复触发终止回调。
- HTTP 200 流在 `[DONE]` 前进入 CLOSED 状态时，按连接提前关闭处理并清理 `isStreaming`。
- 主动停止、替换连接和组件卸载会先标记当前流已结束，再关闭连接，不会把主动关闭误报成异常。

### 3.3 完善非流式请求取消

调整：

- `web/default/src/features/playground/hooks/use-chat-handler.ts`

新增：

- `web/default/src/features/playground/hooks/use-chat-handler.test.tsx`

修复后的行为：

- 组件卸载时递增请求序号，使已经返回的旧请求结果失效。
- 卸载时调用当前 `AbortController.abort()`。
- 发起新非流式请求前先取消仍在执行的旧请求。
- 清空控制器引用，避免后续逻辑误操作已经结束的请求。

### 3.4 消除游乐场重复错误提示

调整：

- `web/default/src/features/playground/api.ts`

模型列表和用户分组请求增加 `skipErrorHandler: true`。HTTP 错误继续抛给 React Query 和业务 Hook，由现有局部逻辑展示一次更明确的错误提示，不再同时触发 Axios 全局 Toast。

### 3.5 将取消信号传播到所有通用上游请求

调整：

- `relay/channel/api_request.go`

新增回归测试：

- `relay/channel/api_request_test.go`

修复后的行为：

- API、表单和异步任务请求统一使用 `http.NewRequestWithContext(c.Request.Context(), ...)`。
- 非流式 padding 分支从入站请求 Context 派生可取消 Context，不再脱离客户端生命周期。
- WebSocket 使用请求 Context 建连，并在客户端 Context 结束时主动关闭底层网络连接；TCP 建连、等待握手响应和已经建立的连接均可被中断。
- 浏览器停止回答、切换页面或断开连接后，网关会尽快取消对应的供应商请求，减少无界后台生成和连接泄漏。

### 3.6 统一错误提示与测试运行器

调整：

- `web/default/src/lib/api.ts`
- `web/default/src/lib/api.test.ts`
- `web/default/src/lib/query-client.ts`
- `web/default/src/components/ui/dropdown-menu.test.tsx`
- `web/default/src/features/dashboard/lib/flow-selection.test.ts`
- `web/default/src/features/dashboard/lib/flow.test.ts`
- `web/default/vitest.config.ts`

修复后的行为：

- Axios API 错误由响应拦截器统一提示，React Query 不再对同一 Axios Mutation 错误重复提示。
- Axios 取消错误不提示 Toast，也不会进入查询重试。
- 304 只产生一条“内容未修改”提示。
- 3 个旧测试统一注册到 Vitest，不再混用 `node:test` 的 suite 注册器。
- Vitest 使用 `threads` 池，避免 Base UI 组件测试在默认 fork 子进程中完成后无法退出。

## 4. 回归测试

前端测试覆盖：

- 网络错误及 502、503、504 可以在上限内重试。
- 500、401、非 Axios 错误、Axios 取消错误和达到重试上限后不再重试。
- 查询 500 不触发登录处理或页面跳转；查询 401 委托给会话失效处理。
- Axios Mutation 错误不会在 React Query 层重复提示。
- 页面卸载会关闭活动 SSE；主动停止不会误报错误。
- HTTP 200 流在 `[DONE]` 前关闭会报告连接中断并结束流状态。
- 同一个流的多个终止事件只上报一次错误，被替换流的延迟事件不会干扰新流。
- 页面卸载会取消活动的非流式请求。
- 原 `node:test` 测试均由 Vitest 正常收集。

后端测试覆盖 API 非流式、API 流式、表单、异步任务和 WebSocket 握手五条通用路径，验证客户端 Context 取消后中继调用会及时返回错误。

关键定向回归结果：7 个前端测试文件、44 项测试全部通过；`relay/channel` 与 `relay/helper` 包测试全部通过。

## 5. 验证情况

已执行并通过：

```text
go test ./relay/channel ./relay/helper
bun run typecheck
bunx oxlint -c .oxlintrc.json <本次涉及的前端文件>
bunx oxfmt --check <本次涉及的前端文件>
bun run test
bun run build
git diff --check
```

全量前端测试结果：9 个测试文件、53 项测试全部通过。原 `No test suite found` 和 fork 子进程不退出问题均已消除。

仓库级 `bun run format:check` 仍会报告一批本次范围外的既有格式问题；本次涉及的 11 个前端文件已单独通过 oxfmt 检查，未批量改写无关文件。

## 6. 审计结论与保留边界

### 6.1 已解决的问题

- 单个查询接口返回 500 不再强制离开游乐场。
- 页面卸载后流式和非流式请求会尽快取消。
- 取消信号从浏览器经过网关传播到 HTTP、表单、任务和 WebSocket 上游连接。
- 并发或连续请求中的旧 SSE 事件不会污染当前回答。
- 200 流提前结束不会再留下永久“回答中”状态。
- 确定性错误不会被多次无效重试。
- 用户主动取消不会触发 React Query 重试。
- 游乐场模型、分组和通用 Mutation 失败不再重复弹出错误提示。
- 全量 Vitest 可稳定完成并正常退出。

### 6.2 仍需生产日志定位底层 500

本次修复消除了“后台接口 500 导致整页回答中断”的前端放大链路，但没有掩盖或修复最初返回 500 的后端根因。仅凭最终 `/500` 页面无法确认具体失败接口。

部署后应重点观察：

- `/api/user/models`
- `/api/user/self/groups`
- `/api/status`
- `/api/notice`
- 聊天中继接口及其上游状态
- 数据库连接池、Redis 和多实例反向代理日志

建议在网关日志中保留请求 ID、接口路径、实例标识、上游渠道、HTTP 状态和耗时，才能将用户截图与最初失败请求准确关联。

### 6.3 扣费边界

本次修复后，取消会从浏览器传播到网关，并由网关取消或关闭供应商 HTTP/WebSocket 连接。该机制能显著缩短客户端离开后的后台生成时间，但取消仍是尽力而为：供应商在观察到断开前可能已经生成内容，也可能按其自身规则上报已发生的用量。

取消不能撤销断开前已经由上游实际生成的内容和成本。当前后端按已消费或最终可确认的 Token 结算，因此部分回答仍可能产生部分费用，这是正常的计费边界；修复目标是避免取消后的无界继续生成，而不是承诺零费用。

不建议对所有客户端断开自动全额退款，否则主动断网即可免费获取已生成内容。如需对平台自身 500 提供补偿，应单独设计基于请求 ID、错误归因和幂等账务流水的补偿策略，并区分：

- 客户端主动停止或离开页面；
- 用户网络中断；
- 平台内部 500；
- 上游在已经产生用量后失败。

### 6.4 发布边界

本次修改需要同时重新构建部署后端服务与 `web/default` 才能完整生效。部署时应检查多实例后端版本和静态资源版本一致性，避免旧 HTML、旧入口脚本或旧实例继续保留原全局 500 跳转或旧的上游请求生命周期逻辑。

## 7. 2026-07-15 生产上线复审

本轮按生产门槛再次审计前端事件顺序、后端取消语义、长轮询、WebSocket、渠道重试和资源释放，补充修复如下：

- SSE 使用 `start: false` 创建实例，先注册 `message`、`error`、`readystatechange` 监听器，再显式调用 `stream()`，避免极端情况下漏掉启动阶段的首个错误；构造失败和同步启动失败均会结束“回答中”状态。
- 通用上游 HTTP、Form、任务及 WebSocket 请求在客户端取消时统一返回 `client_disconnected`（499、禁止重试、禁止错误日志记录），不再被误判为普通渠道故障，避免无效重试和误封健康渠道。
- 控制器在渠道分发前、渠道处理后和最终错误输出前均复核请求 Context，作为供应商适配器遗漏取消分类时的防御层。
- 非流式 padding 请求的派生 Context 绑定到响应体关闭：失败路径立即取消，成功路径在上游响应体关闭时释放，避免 Context 泄漏，也不会在响应体读取完成前过早取消。
- 新增通用 Context 绑定 WebSocket 拨号器；除 TCP 建连外，TLS/HTTP Upgrade 握手等待和已建立连接上的阻塞读取也会在请求取消时被底层连接关闭打断。
- Dify 文件上传、Replicate 文件上传、Ali 同步图片任务轮询、Volcengine TTS WebSocket、Xunfei WebSocket、Baidu access token、Midjourney 提交与图片转发均接入请求 Context 或明确的超时边界。
- Ali 轮询等待改为可取消计时器，并修复连续查询失败时绕过最大次数、无限轮询的问题。
- 流式 ping 写失败后立即停止扫描链路，避免客户端已不可写时仍等待上游输出。
- 旧渠道适配器中未实现的 Claude 转换由 `panic` 改为普通“不支持”错误，避免异常请求触发 Gin 500 恢复页。
- `CustomEvent` 删除无效的值拷贝 Mutex；此前该锁随 Render 值接收器复制，既不能提供共享互斥，又会触发 `go vet` 的 copylock 风险。

新增或加强的回归覆盖包括：

- SSE 监听器先于启动注册、构造失败、启动失败、提前 EOF、重复终止事件、主动停止、卸载清理和旧流延迟事件隔离；
- HTTP 流式/非流式、Form、任务、padding 和 WebSocket 的客户端取消分类；
- padding 响应体关闭后释放上游 Context；
- 已建立 WebSocket 在 Context 取消后结束双方连接；
- Baidu access token、Midjourney 请求和 Ali 轮询继承取消信号。

最终上线门禁：

```text
go test ./...
go vet ./...
bun run typecheck
bun run test
bun run build:check
bunx oxlint -c .oxlintrc.json <本功能相关文件>
bunx oxfmt --check <本功能相关文件>
git diff --check
```

上述功能相关测试、类型检查、生产构建、静态检查均通过。当前 Windows 环境未安装 GCC，无法启用 Go `-race`；正式发布流水线如具备 CGO 工具链，建议补跑定向竞态测试。

仓库级 `bun run lint`、`bun run copyright:check`、`bun run format:check` 仍会报告大量本功能范围外的既有文件；本功能涉及的文件已单独通过 lint、格式与版权头检查。如果发布 CI 将这三个仓库级脚本设为硬门禁，需要另行清理全仓历史前端质量债务后再合并，不能把这些既有失败误归因于本次游乐场修复。

# Web Default 模型定价保存竞态修复

> 日期：2026-07-09  
> 范围：`web/default` 系统设置中的 `system-settings/billing/model-pricing` 模型定价保存流程。  
> 目标：修复模型定价连续保存时旧配置覆盖新配置的问题，降低多实例部署下读取旧配置导致的保存丢失风险，并优化批量保存提示体验。

## 1. 背景

模型定价页面支持用可视化方式编辑单个模型的按量计费配置。一个模型可能同时涉及多个系统配置项，例如：

- `ModelPrice`
- `ModelRatio`
- `CacheRatio`
- `CreateCacheRatio`
- `CompletionRatio`
- `ImageRatio`
- `AudioRatio`
- `AudioCompletionRatio`
- `billing_setting.billing_mode`
- `billing_setting.billing_expr`

原流程中，前端会把这些配置分别作为独立 key 逐个调用 `PUT /api/option/` 保存。每个 key 保存成功后都会触发 `system-options` 重新拉取，导致以下问题：

- 连续保存多个模型时，后一次拉取可能拿到前一次保存前的旧快照。
- 表单会被旧快照无条件 `reset`，刚保存的模型在界面上消失。
- 用户继续保存下一个模型时，前端会用旧表单状态全量覆盖对应 JSON map，导致上一次保存真正从数据库中丢失。
- 一次按量配置保存会弹出多条“设置更新成功”，影响操作体验。

## 2. 多实例场景影响

多实例部署即使连接同一台数据库，也仍然存在写后读旧值的窗口。原因是系统配置不仅存在数据库中，也缓存在各实例进程内存的 `OptionMap` 中：

- `PUT /api/option/` 写库后只更新处理该请求的实例内存。
- `GET /api/option/` 返回当前实例内存里的配置，而不是每次从数据库读取。
- 其他实例依赖 `SyncOptions` 周期性从数据库同步，周期由 `SYNC_FREQUENCY` 控制，默认 60 秒。

因此在负载均衡下，保存后立即拉取配置可能落到尚未同步的实例，拿到旧配置。旧配置再被前端当作最新状态写回时，就会覆盖刚保存的模型定价。

本次前端修复后，保存成功不再依赖立即重新拉取配置，而是直接把已确认写入的 key 更新到 React Query 缓存，从而避开多实例写后读旧值造成的前端回退。

仍需注意：

- 其他实例的实际计费配置仍会按 `SYNC_FREQUENCY` 延迟生效。
- 如果保存后在同步周期内刷新页面，并且请求落到未同步实例，页面仍可能短暂看到旧配置。
- 若要彻底保证多实例写后读一致，需要后端将 `GET /api/option/` 改为读库，或提供批量保存接口并配合版本校验。

## 3. 已落地修改

### 3.1 配置拉取绕过 GET 去重

涉及文件：

- `web/default/src/features/system-settings/api.ts`

修改内容：

- `getSystemOptions()` 调用 `api.get('/api/option/')` 时增加 `disableDuplicate: true`。
- 避免 React Query refetch 复用写入前已经在途的旧 GET 响应。
- 防止旧响应被缓存成新的 `system-options` 数据。

### 3.2 批量保存时跳过逐 key 刷新

涉及文件：

- `web/default/src/features/system-settings/hooks/use-update-option.ts`
- `web/default/src/features/system-settings/models/ratio-settings-card.tsx`

修改内容：

- `useUpdateOption` 新增配置：
  - `skipInvalidate`
  - `skipSuccessToast`
- 模型定价卡片启用 `skipInvalidate`，保存多个相关 key 时不再每个 key 成功后都触发 `system-options` refetch。
- 每个 key 保存成功后记录已写入的值；整批保存完成后通过 `queryClient.setQueryData` 直接更新本地缓存。
- 如果中途某个 key 保存失败：
  - 已成功写入的 key 仍同步到缓存。
  - 表单保持 dirty，方便用户重试。
  - 不会把失败的值写入本地保存基线。

### 3.3 表单 reset 增加状态保护

涉及文件：

- `web/default/src/features/system-settings/models/ratio-settings-card.tsx`

修改内容：

- `modelDefaults` / `groupDefaults` 每次父组件渲染都会生成新对象，原先 effect 只看对象引用会频繁触发 `reset`。
- 新逻辑先将服务端默认值 normalize 后，与本地保存基线逐字段比较；值未变化时不 reset。
- 当模型表单或分组表单存在未保存编辑时，不允许后台 refetch 结果覆盖表单。
- 当保存正在进行中时，不允许中途 refetch 覆盖表单。
- “Reset prices” 属于用户明确要求用服务端重置值覆盖本地表单，因此通过一次性 `forceServerSyncRef` 放行同步。

### 3.4 保存成功提示合并

涉及文件：

- `web/default/src/features/system-settings/hooks/use-update-option.ts`
- `web/default/src/features/system-settings/models/ratio-settings-card.tsx`

修改内容：

- 批量保存时禁用每个 key 的成功 toast。
- 模型定价和分组倍率整批保存成功后，仅显示一条“设置更新成功”。
- 错误提示不合并，仍保留每个失败请求的明确错误反馈。

## 4. 当前行为

- 连续保存多个模型时，前一个模型的保存结果不会被后一次旧配置拉取覆盖。
- 在可视化编辑器中填写多个输入框后保存，内部仍会按变更 key 逐个调用接口，但不会逐个弹成功提示。
- 保存成功后，页面本地缓存立即反映刚写入的配置，不等待多实例同步。
- 后台配置 refetch 不会覆盖用户正在编辑的草稿。

## 5. 后续建议

- 后端可基于现有 `model.UpdateOptionsBulk` 增加批量保存 HTTP 接口，使模型定价一次保存只产生一次事务提交。
- 若需要彻底解决多实例写后读旧值，建议：
  - 将 `GET /api/option/` 改为读数据库，或
  - 为系统设置增加版本号 / 更新时间，前端保存时携带版本做乐观锁校验，避免旧快照覆盖新快照。
- 生产环境如希望缩短配置生效延迟，可适当调小 `SYNC_FREQUENCY`，同时评估所有实例同步查询对数据库的压力。

## 6. 验证情况

- `web/default` 下执行 `bun run typecheck` 通过。
- 对修改文件执行 `oxlint` 通过，无新增 lint error。
- 执行 `oxfmt` 格式化相关文件。

# Web Default 游乐场聊天配置与备份迁移

> 日期：2026-07-10  
> 范围：`web/default` 游乐场消息布局、聊天参数设置、浏览器本地持久化与备份导入导出。  
> 目标：优化消息阅读排版，补齐旧版 `web/classic` 操练场已有的聊天配置和备份能力，并确保旧版浏览器数据能够安全迁移且不会被覆盖。

## 1. 消息布局优化

### 1.1 问题现象

游乐场原先默认采用交错布局，用户消息位于右侧且正文也使用右对齐。长文本或多行内容右对齐后阅读体验较差，操作按钮与时间信息的排布也显得拥挤。

### 1.2 修改内容

涉及文件：

- `web/default/src/features/playground/components/chat/playground-chat.tsx`
- `web/default/src/features/playground/components/message/playground-message-content.tsx`
- `web/default/src/features/playground/lib/message/message-layout-utils.ts`
- `web/default/src/features/playground/lib/message/message-styles.ts`

调整内容：

- 游乐场消息默认改为左对齐布局。
- 保留交错布局能力；当消息容器位于右侧时，气泡位置仍靠右，但多行正文内部保持左对齐。
- 用户气泡圆角会根据消息所在方向调整，左侧布局使用左下角收口，右侧布局使用右下角收口。

## 2. 聊天参数设置

### 2.1 功能入口

输入框工具栏新增“聊天设置”按钮，点击后从右侧打开设置面板。面板在桌面端和移动端共用同一套响应式布局。

涉及文件：

- `web/default/src/features/playground/components/settings/playground-settings.tsx`
- `web/default/src/features/playground/components/input/playground-input-tools.tsx`
- `web/default/src/features/playground/components/input/playground-input.tsx`
- `web/default/src/features/playground/index.tsx`

### 2.2 支持参数

设置面板接入了 `web/default` 已有但此前没有 UI 的配置状态：

- `temperature`：0～2，步进 0.1
- `top_p`：0～1，步进 0.1
- `frequency_penalty`：-2～2，步进 0.1
- `presence_penalty`：-2～2，步进 0.1
- `max_tokens`
- `seed`
- `stream`

除 `stream` 外，每个可选请求参数均保留独立启用开关。关闭开关后参数值仍会保留，但不会写入聊天请求。配置变更继续通过既有 `usePlaygroundState` 自动写入浏览器本地存储。

设置面板提供“重置聊天设置”，用于恢复 `DEFAULT_CONFIG` 与 `DEFAULT_PARAMETER_ENABLED`，不会清空当前对话记录。

### 2.3 参数文案

中文环境下，常见采样参数采用“英文术语（中文说明）”形式展示：

- `Temperature (温度)`
- `Top P (核采样)`
- `Frequency Penalty (频率惩罚)`
- `Presence Penalty (存在惩罚)`

新增文案已覆盖 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`，并通过项目 i18n 同步脚本统一写入和排序。

## 3. 备份导入与导出

### 3.1 导出内容

聊天设置面板新增“备份与恢复”区域。导出的 JSON 文件包含：

- 当前模型、分组及聊天参数配置
- 每个可选参数的启用状态
- 当前全部对话记录
- 备份格式标识、版本号和导出时间

新版备份格式：

```json
{
  "format": "new-api-playground-backup",
  "version": 1,
  "exportedAt": "2026-07-10T00:00:00.000Z",
  "config": {},
  "parameterEnabled": {},
  "messages": []
}
```

导出文件名采用 `playground-backup-YYYY-MM-DD.json`。

### 3.2 导入行为

涉及文件：

- `web/default/src/features/playground/components/settings/playground-backup-controls.tsx`
- `web/default/src/features/playground/lib/storage/storage.ts`
- `web/default/src/features/playground/hooks/use-playground-state.ts`

导入流程：

- 仅接受 JSON 文件，并限制文件大小，避免读取异常大文件。
- 在写入前校验配置、参数开关与消息结构。
- 导入前弹出确认提示，明确说明将覆盖当前聊天设置和对话记录。
- 导入成功后同步更新 React 状态与新版 localStorage 数据。
- 导入过程中会取消尚未执行的消息防抖保存任务，避免旧消息在导入后再次写回并覆盖导入结果。

### 3.3 Classic 备份兼容

新版导入器兼容 `web/classic` 操练场导出的旧格式：

```json
{
  "inputs": {},
  "parameterEnabled": {},
  "messages": [],
  "exportTime": "2026-07-10T00:00:00.000Z",
  "version": "1.0"
}
```

兼容处理包括：

- 将旧版 `inputs` 转换为新版扁平 `PlaygroundConfig`。
- 迁移旧版 `parameterEnabled`。
- 将旧版 `{ role, content }` 消息转换为新版 `{ from, versions }` 消息。
- 兼容旧版字符串形式的数值参数。
- 对导入消息执行数量、内容长度、状态和存储 schema 校验。

因此，用户可以直接在 `web/classic` 操练场导出 JSON，再在 `web/default` 游乐场中导入。

## 4. 浏览器本地数据兼容与覆盖规则

Classic 与 Default 使用不同的 localStorage key，JSON 结构也不同。

Classic 使用：

- `playground_config`
- `playground_messages`
- `playground_parameter_enabled`

Default 使用：

- `default_playground_config:v1`
- `default_playground_messages:v1`
- `default_playground_parameter_enabled:v1`

Default 首次加载且找不到新版 key 时，会读取 Classic key，完成格式转换后复制到新版 key。迁移过程不会删除、清空或改写 Classic key，因此旧操练场数据不会被新版游乐场覆盖。

### 4.1 初始化覆盖问题复盘与修复

问题根因：

- 旧版 `web/classic` 操练场与新版 `web/default` 游乐场曾共用 `playground_config` / `playground_messages`。
- Classic 消息格式是 `{ messages: [...] }`，单条消息使用 `role/id/content/reasoningContent`。
- Default 消息格式是 `{ version, data }`，单条消息使用 `from/key/versions/reasoning`。
- 新版初始化读取旧 key 时无法通过新版 schema 校验，会表现为“历史消息为空”；用户在新版发送、删除或清空消息后，新版会把同名 key 写成新格式，导致 Classic 旧数据被覆盖。
- 旧实现还会在 `playground_messages` 超过大小阈值时直接删除该 key，大历史用户存在一打开新版就丢失旧记录的风险。

本次修复：

- `web/default/src/features/playground/constants.ts`：Default 改用 `default_playground_*:v1` 私有 key，并保留 Classic key 只用于迁移读取。
- `web/default/src/features/playground/lib/storage/storage.ts`：初始化优先读取新版 key；新版 key 不存在时再尝试读取 Classic key 并转换格式。
- 迁移只写入新版 key，不删除、不清空、不覆盖 Classic key。
- Classic 旧消息即使超过新版存储大小阈值，也不会被新版删除；Default 会尽量读取、裁剪并迁移到新版 key。
- 如果新版 key 损坏，会继续尝试从 Classic key 初始化，避免坏数据阻断迁移。

恢复边界：

- 对尚未被新版覆盖的浏览器，升级后可自动迁移旧操练场数据。
- 对已经被同名 key 覆盖的浏览器，前端无法从 localStorage 自动恢复原始 Classic 数据，只能依赖用户此前导出的备份、浏览器 Profile 备份或其它外部备份。

需要注意：

- 一旦新版 key 已生成，Default 会优先读取新版数据，不再自动跟随 Classic 后续产生的新记录。
- 如果迁移后继续在 Classic 中聊天，需要从 Classic 导出最新备份，再到 Default 导入。
- Default 的清空和重置操作只处理新版 key，不会删除 Classic key。

## 5. 验证情况

- `web/default` 下执行 `bun run typecheck` 通过。
- 对涉及的 TypeScript / TSX 文件执行 `oxlint`，无 lint error。
- 对新增和修改文件执行 `oxfmt`。
- 执行 `bun run i18n:sync`，所有语言缺失键为 0。
- 使用 Classic 格式样例验证旧消息与配置转换，转换通过。
- 执行 `bun run build`，生产构建通过。

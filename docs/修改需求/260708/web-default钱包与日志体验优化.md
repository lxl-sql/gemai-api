# Web Default 钱包与日志体验优化

> 日期：2026-07-08  
> 范围：`web/default` 前端体验、使用日志展示、钱包充值展示、命令搜索入口，以及日志敏感字段后端兜底。  
> 目标：让普通用户更容易理解钱包/订单/日志信息，同时避免暴露实际上游模型等敏感信息。

## 1. 背景

本次修改来自 `web/default` 与旧版 `web/classic` 的体验差异排查。主要问题集中在：

- 钱包页部分按钮、折扣、订单状态仍显示英文或不符合中文语境。
- 充值折扣价格展示不够直观，缺少货币符号、原价划线、现价突出。
- 使用日志移动端未明显展示流式/非流式、首字响应、客户端断开等信息。
- 日志详情入口不够明显，详情弹窗字段命名与旧版不一致。
- 普通用户可见实际上游模型，存在模型映射隐私泄露风险。
- 移动端流异常红色感叹号使用 tooltip，不适合触屏点击。
- 顶部导航入口未加入全局命令搜索。

## 2. 已落地修改

### 2.1 钱包页翻译与折扣展示

涉及文件：

- `web/default/src/features/wallet/components/recharge-form-card.tsx`
- `web/default/src/features/wallet/index.tsx`
- `web/default/src/features/wallet/lib/billing.ts`
- `web/default/src/features/wallet/types.ts`
- `web/default/src/features/wallet/components/dialogs/billing-history-dialog.tsx`
- `web/default/src/i18n/locales/*.json`

修改内容：

- 充值按钮、节省金额、折扣标签改为 i18n 文案，不再固定显示 `Pay`、`Save`、`OFF`。
- 折扣展示改为“现价突出 + 原价小号划线”的形式。
- 价格前增加货币符号：
  - 美元显示 `$`
  - 人民币显示 `¥`
  - 其他币种显示系统设置中的自定义符号
- 折扣标签使用红色，节省金额使用绿色。
- 订单历史状态改为根据翻译展示，不再直接显示 `Pending`。
- 补齐 `failed` 订单状态类型和展示配置。
- 补充部分支付方式显示名，如 `Creem`、`Waffo Pancake`、`Balance`。

### 2.2 使用日志列表与移动端展示

涉及文件：

- `web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx`
- `web/default/src/features/usage-logs/components/usage-logs-mobile-card.tsx`
- `web/default/src/features/usage-logs/lib/format.ts`
- `web/default/src/features/usage-logs/types.ts`

修改内容：

- 移动端“耗时”字段不再只显示第一行，补充展示：
  - 流式 / 非流式
  - 首字响应时间
  - `t/s` 输出速度
  - 流异常红色感叹号
- 桌面端详情入口去掉“查看详情”文字，避免占用列宽；移动端保留眼睛图标 + 边框，更容易点击。
- 流异常红色感叹号从 `Tooltip` 改为可点击 `Popover`：
  - 解决触屏设备无法可靠点击的问题。
  - 点击区域扩大，不再只依赖小 SVG。
  - 弹层改成类似旧版 `web/classic` 的黑底提示。
- 异常提示去掉重复标题，只展示真正原因，例如：
  - 客户端断开
  - `context canceled`
  - 软错误数量
  - 结束错误
- 异常提示层级单独降低，避免覆盖顶部 header/nav。

### 2.3 日志详情弹窗信息结构

涉及文件：

- `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`

修改内容：

- 移除顶部三块摘要卡片：
  - 请求方式
  - 首字响应
  - 客户端状态
- 将耗时信息合并到一行展示，并统一命名为 `Response Time` / 响应时间，和旧版语义保持一致。
- 响应时间行同时展示：
  - 总响应耗时
  - 首字响应
  - 流式 / 非流式
- `请求传输` 区域只在异常时展示；正常完成时隐藏，避免重复信息干扰用户。
- 客户端状态在详情弹窗内改为普通小字号彩色文本，不再使用大号徽标样式。
- 异常时展示客户端状态、结束原因、结束错误等关键信息。

### 2.4 模型映射隐私保护

涉及文件：

- `web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx`
- `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`
- `model/log.go`

修改内容：

- 普通用户不再看到实际上游模型名。
- 管理员仍可在模型映射中查看请求模型与实际模型。
- 后端 `formatUserLogs` 对普通用户额外移除 `upstream_model_name`，避免仅靠前端隐藏造成泄露。
- 普通用户仍可看到必要的流状态摘要，但隐藏内部 `stream_status.errors` 错误数组。

### 2.5 顶部导航加入命令搜索

涉及文件：

- `web/default/src/components/command-menu.tsx`
- `web/default/src/hooks/use-top-nav-links.ts`

修改内容：

- 全局命令搜索新增“Header navigation / 顶部导航”分组。
- 搜索中加入顶部导航实际启用的入口：
  - 主页
  - 控制台
  - 模型广场
  - 排行榜
  - 文档
  - 关于
- 复用 `useTopNavLinks()`，保证搜索入口与顶部导航配置一致。
- 外部文档链接在新窗口打开。
- 需要登录的入口跳转到登录页，并带上 `redirect`。

## 3. 交互原则

- 移动端优先保证可点击性，不依赖 hover-only tooltip。
- 普通用户只看能理解的信息，管理员才看排障和真实上游细节。
- 正常状态少展示，异常状态重点展示。
- 命名和旧版语义保持一致，避免同一字段出现“总耗时 / 响应时间”等不同叫法。
- 价格和折扣信息优先让用户一眼看懂：现价大、原价小且划线、节省金额绿色、折扣红色。

## 4. 验证情况

- `web/default` 下多次执行 `bun run typecheck`，均通过。
- 已对相关 TS/TSX 文件执行 IDE 诊断读取，无新增 linter 报错。

## 5. 后续建议

- 若继续优化日志体验，可将流异常原因整理为更面向普通用户的短文案，例如“网络中断”“客户端取消请求”等。
- 若需要完全复刻 `web/classic` 的日志移动端样式，可单独整理一份视觉对照表，再统一调整卡片密度、颜色和字段顺序。

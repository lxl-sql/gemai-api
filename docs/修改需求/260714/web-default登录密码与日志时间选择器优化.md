# Web Default 登录密码与日志时间选择器优化

> 日期：2026-07-14  
> 范围：`web/default` 登录密码框、通用日志时间筛选、操作日志与额度流水时间筛选  
> 目标：修复密码可见性按钮闪动与点击体验问题，并将浏览器原生日期时间输入升级为可复用、可联动的双月日期时间范围组件。

## 1. 问题背景

### 1.1 登录密码框

密码框右侧的可见性按钮原先在点击时直接替换“眼睛 / 闭眼”图标，会出现瞬间闪动和视觉跳变。同时，按钮可点击区域较小，图标与输入框的比例不够清晰。

### 1.2 日志时间筛选

原有 `CompactDateTimeRangePicker` 依赖浏览器原生 `datetime-local` 控件，主要存在以下问题：

- 不同浏览器和操作系统的弹层样式不一致，无法与项目视觉体系统一。
- 起始日期与结束日期是两个孤立输入，缺少范围选择和有效性联动。
- 日期、时间、快捷范围和确认操作的层级不清晰。
- 旧组件位于使用日志功能目录，但已被操作日志和额度流水复用，组件归属与实际职责不匹配。

## 2. 已落地修改

### 2.1 密码可见性按钮

调整文件：

- `web/default/src/components/password-input.tsx`

修改内容：

- “眼睛”和“闭眼”图标同时固定在按钮内，通过透明度、缩放和轻微旋转完成交叉过渡，不再在点击时瞬间替换 DOM 位置。
- 动画时长设为 200ms，并支持 `prefers-reduced-motion`，用户开启减少动效后不强制播放过渡。
- 按钮尺寸调整为 `icon-sm`，改善可点击区域，并统一图标的视觉尺寸和对齐。
- 在 `mousedown` 阶段阻止默认行为，避免切换密码可见性时输入框焦点丢失。
- 保留国际化的 `aria-label`，不改变键盘与辅助技术的访问方式。

### 2.2 抽取通用日期时间范围组件

新增目录：

- `web/default/src/components/date-time-range-picker/`

将日期时间筛选从业务功能目录迁移到通用组件层，并按职责拆分：

- `date-time-range-picker.tsx`：组件入口，管理弹层、草稿范围、提交、清空和响应式布局。
- `date-range-calendar.tsx`：单实例双月范围日历、年/月导航头与日期状态样式。
- `time-select.tsx`：起始和结束时间的小时、分钟选择与边界约束。
- `presets.ts`：默认快捷范围定义。
- `date-utils.ts`：日历语言、日期复制、同日判断和时间边界归一化。
- `types.ts` / `index.ts`：对外类型与公开出口。
- `date-utils.test.ts`：关键日期、时间边界和本地化规则的单元测试。
- `date-time-range-picker.test.tsx`：跨月范围、焦点迁移、响应式布局和时间菜单的组件回归测试。

组件继续使用受控接口：外部传入 `start` / `end`，仅在确认、清空或快捷选择时通过 `onChange` 提交，使组件与日志查询、URL 状态和表格实现解耦。

### 2.3 双月日历与年/月导航

参考 `web/classic` 日志时间查询的交互，桌面端由一个 DayPicker 实例同时渲染左右两个月份面板，移动端复用同一组件的单月布局。范围选择、焦点、键盘导航和 roving tabindex 全部由同一实例管理，避免跨月选择后左右日历各自保留焦点。

导航规则：

- `«` / `»` 语义对应上一年和下一年。
- `‹` / `›` 语义对应上一个月和下一个月。
- 月份和年份仅作为标题展示，不再使用下拉框筛选。
- 左右月份面板共享同一个范围和焦点状态，可跨月选择起止日期。
- 导航限制为当前年往前 20 年至当前年往后 1 年，到边界后对应按钮禁用。
- 双月布局保证右侧始终是左侧的下一个月，避免两个日历出现重复或顺序混乱。
- 隐藏非当月的相邻日期，保留占位但不在左右面板重复显示同一日期。
- 日期按钮保持紧凑的固定圆形尺寸，星期与日期七列在月份面板内等距铺满，消除居中窄网格造成的大块左右留白；日期单元格与范围背景分层渲染，范围背景使用对比度更高的语义色并在每周内连续衔接，起止日按钮保留黑色实心圆形，不会因背景铺满而被拉伸成椭圆。
- 鼠标点选不额外展示 DayPicker 内部焦点环；键盘 `focus-visible` 仍保留明确的焦点提示。

### 2.4 日期与时间联动

- 点选日期时保留已选的小时和分钟。
- 首次选择起始日期默认为 `00:00:00.000`，结束日期默认为 `23:59:59.999`。
- 手动选择结束分钟后，结束值归一化为该分钟的 `59.999` 秒，避免查询丢失该分钟内的日志。
- 当起止日期是同一天时，时间下拉项根据另一侧边界动态禁用无效值，防止选出结束时间早于起始时间的范围；边界小时只要仍包含至少一个有效分钟就保持可选，再由分钟列表精确限制可用范围。切换到边界小时时，如果当前分钟超出有效区间，会自动收敛到最接近的有效分钟。
- 草稿范围不完整或起始时间晚于结束时间时，确认按钮禁用。
- 关闭弹层而未确认时不会修改外部查询条件，再次打开时从已提交值重新初始化。
- 小时与分钟下拉列表固定为 14rem 高度，超出部分在列表内部滚动，不再随可用视口高度自适应拉长。
- 窄屏下起始时间和结束时间改为上下排列，避免两个时间控件在 320–360px 宽度内挤压或溢出；较宽屏幕继续并排显示。

### 2.5 快捷范围与本地化

内置快捷范围：

- 今天
- 近 7 天
- 本周
- 近 30 天
- 本月

日历按参考交互统一以星期日作为每周第一列，“本周”快捷范围同步使用相同起始规则，避免日历排列与查询范围不一致。日历已适配 `en`、`zh`、`zh-TW`、`fr`、`ru`、`ja`、`vi`，未匹配语言回退到英文日历。

i18next 内部使用的 `zhCN` / `zhTW` 会先经由项目统一的 `toIntlLocale()` 转换为 `zh-CN` / `zh-TW`，再传入 `Intl.DateTimeFormat`，避免打开日历时因非标准语言标签触发 `RangeError`。日历区域配置同步使用转换后的标准标签，简体中文使用 `zhCN` 日历，繁体中文使用独立的 `zhTW` 日历，不再共用简体区域配置。

本次同步补充了“上一年”、“上一个月”、“下一个月”和“下一年”的全语言无障碍文案，并将中文“This month”翻译从特定页面语义的“本月获得”纠正为通用的“本月”。

### 2.6 接入范围

新组件已替换以下页面中的旧时间选择器：

- 通用日志
- 任务日志
- 操作日志
- 额度流水

原文件 `web/default/src/features/usage-logs/components/compact-date-time-range-picker.tsx` 已删除，避免新旧两套日期范围逻辑并存。

### 2.7 审计修复与测试补齐

完成组件审计后补充处理以下问题：

- 修复窄屏下起止时间强制双列导致的控件挤压和横向溢出。
- 修复小时选项使用“当前分钟”直接判断边界、导致整个边界小时被误禁用的问题，并在切换小时后自动校正分钟。
- 将繁体中文日历从 `zhCN` 调整为独立的 `zhTW` 区域配置，并纠正繁体中文“本月”文案。
- 为项目补充 Vitest、Testing Library 和 jsdom 测试环境，测试入口统一为 `bun run test`。
- 新增日期时间范围选择器组件测试，覆盖跨月范围连续性、左右日历焦点迁移、时间边界、窄屏布局以及下拉菜单固定高度滚动。

## 3. 组件边界与解耦原则

- 通用组件只负责编辑和返回日期时间范围，不直接发起日志请求，也不依赖具体表格和路由状态。
- 快捷范围通过 `presets` 属性可替换，业务页后续可按需定制，无需修改组件内核。
- 日期工具、快捷范围、月份面板和时间选择各自保持单一职责，避免重新形成单个超大组件。
- 组件公开的日期值使用 `Date` 对象，不在通用层绑定 API 的 Unix 时间戳或字符串格式。
- 桌面端与移动端共享同一套状态和校验逻辑，仅在日历面板数量和布局上响应式切换。

## 4. 涉及文件

- `web/default/src/components/password-input.tsx`
- `web/default/src/components/date-time-range-picker/date-time-range-picker.tsx`
- `web/default/src/components/date-time-range-picker/date-range-calendar.tsx`
- `web/default/src/components/date-time-range-picker/time-select.tsx`
- `web/default/src/components/date-time-range-picker/date-utils.ts`
- `web/default/src/components/date-time-range-picker/date-utils.test.ts`
- `web/default/src/components/date-time-range-picker/date-time-range-picker.test.tsx`
- `web/default/src/components/date-time-range-picker/presets.ts`
- `web/default/src/components/date-time-range-picker/types.ts`
- `web/default/src/components/date-time-range-picker/index.ts`
- `web/default/src/features/usage-logs/components/common-logs-filter-bar.tsx`
- `web/default/src/features/usage-logs/components/task-logs-filter-bar.tsx`
- `web/default/src/features/usage-logs/components/compact-date-time-range-picker.tsx`（删除）
- `web/default/src/features/operation-logs/components/operation-logs-table.tsx`
- `web/default/src/features/quota-transactions/components/quota-transactions-table.tsx`
- `web/default/src/i18n/locales/*.json`
- `web/default/src/test/setup.ts`
- `web/default/vitest.config.ts`
- `web/default/package.json`
- `web/bun.lock`

## 5. 验证情况

已对相关修改执行：

```text
bun run typecheck
bunx oxlint -c .oxlintrc.json src/components/date-time-range-picker src/test vitest.config.ts
bun run test -- src/components/date-time-range-picker
bunx oxfmt --check src/components/date-time-range-picker src/test vitest.config.ts
bun run build
```

以上检查均已通过，日期时间范围选择器共有 9 项回归测试，覆盖分钟边界归一化、同日判断、边界小时与分钟、简繁体区域语言、本周起始规则、跨月范围、单一焦点状态、窄屏时间布局以及固定高度滚动菜单。

## 6. 保留边界

- 本次仅调整前端选择与展示交互，不改变日志查询接口、时间戳传递方式和后端查询语义。
- 时间依然使用浏览器当前时区构造 `Date` 对象，与现有日志查询行为保持一致。
- 触发器文案继续只展示到分钟，但内部结束时间保留到该分钟末尾，保证显示精度与查询覆盖范围同时满足。

# Web Default 命令搜索菜单权限修复

> 日期：2026-07-14  
> 范围：`web/default` 顶部命令搜索、根侧边栏菜单过滤、搜索上下文  
> 目标：确保命令搜索与侧边栏使用一致的菜单可见性规则，避免普通用户看到管理员菜单入口。

## 1. 问题现象

普通用户打开顶部命令搜索后，搜索结果中会显示完整的“管理员”菜单组，包括：

- 渠道
- 模型
- 用户
- 兑换码
- 订阅管理
- OAuth 应用
- 系统信息
- 系统设置

侧边栏本身不会向普通用户显示这些入口，但命令搜索仍会展示，导致两个导航入口的权限表现不一致。即使后端和路由仍会独立校验权限，向无权限用户展示不可访问入口也会造成误导，并暴露不属于当前角色的管理功能结构。

## 2. 原因分析

根侧边栏通过 `useSidebarView` 对原始菜单执行了以下过滤：

1. 根据 `sidebar_modules` 应用管理员配置与用户配置。
2. 普通用户隐藏整个 `admin` 菜单组。
3. 根据菜单项的 `requiredRole` 过滤超级管理员专属入口。

命令搜索原先直接调用 `useSidebarData()` 读取未经权限处理的原始菜单：

```ts
const sidebarData = useSidebarData()
const navGroups = getNavGroupsForPath(pathname, t) ?? sidebarData.navGroups
```

因此，侧边栏与命令搜索形成了两套不同的数据链路：侧边栏使用过滤后的菜单，命令搜索使用完整菜单。问题不是搜索匹配算法错误，而是搜索数据源绕过了角色和模块可见性过滤。

## 3. 修复方案

### 3.1 提取根菜单统一过滤 Hook

新增：

- `web/default/src/hooks/use-root-sidebar-groups.ts`

将根侧边栏原有的模块配置、管理员菜单组和 `requiredRole` 过滤逻辑提取为 `useRootSidebarGroups()`。该 Hook 返回当前用户实际可见的根菜单组，作为侧边栏与命令搜索的共同数据源。

当前规则保持不变：

- 未登录角色按访客处理。
- 角色低于管理员时隐藏整个 `admin` 菜单组。
- 菜单项设置 `requiredRole` 时，仅向达到对应角色的用户显示。
- `sidebar_modules` 配置仍可继续缩小可见范围。

### 3.2 侧边栏复用统一过滤结果

调整：

- `web/default/src/hooks/use-sidebar-view.ts`

`useSidebarView()` 不再自行维护一份根菜单过滤实现，改为直接使用 `useRootSidebarGroups()`。嵌套工作区菜单的解析方式保持不变。

### 3.3 命令搜索改用可见菜单

调整：

- `web/default/src/components/command-menu.tsx`

命令搜索在当前页面不属于嵌套工作区时，回退到 `useRootSidebarGroups()` 返回的可见菜单，而不是原始 `useSidebarData()` 数据。

修复后的行为：

- 普通用户搜索不到管理员菜单组及其入口。
- 管理员仍可搜索管理员菜单。
- 仅超级管理员可搜索带 `requiredRole: ROLE.SUPER_ADMIN` 的入口。
- 管理员或用户通过 `sidebar_modules` 隐藏的模块不会重新出现在搜索中。
- 系统设置等嵌套工作区仍使用当前工作区的导航数据。

### 3.4 拆分搜索上下文，消除循环依赖

新增：

- `web/default/src/context/search-context.ts`

调整：

- `web/default/src/context/search-provider.tsx`
- `web/default/src/components/search.tsx`
- `web/default/src/components/command-menu.tsx`

原结构中 `SearchProvider` 引入 `CommandMenu`，而 `CommandMenu` 又从 `search-provider.tsx` 引入 `useSearch`，形成模块循环依赖。现将 `SearchContext` 与 `useSearch` 拆分到独立文件，Provider 和消费组件都从该文件读取上下文，避免循环引用。

同时将命令项的数组索引 key 改为由菜单 URL 等稳定业务字段构成的 key，并补齐条件分支花括号，以满足当前 React lint 规则。

## 4. 涉及文件

- `web/default/src/components/command-menu.tsx`
- `web/default/src/components/search.tsx`
- `web/default/src/context/search-context.ts`
- `web/default/src/context/search-provider.tsx`
- `web/default/src/hooks/use-root-sidebar-groups.ts`
- `web/default/src/hooks/use-sidebar-view.ts`

## 5. 验证情况

已执行：

```text
bunx oxlint -c .oxlintrc.json src/components/command-menu.tsx src/components/search.tsx src/context/search-context.ts src/context/search-provider.tsx src/hooks/use-sidebar-view.ts src/hooks/use-root-sidebar-groups.ts
bun run typecheck
bunx oxfmt --check src/components/command-menu.tsx src/components/search.tsx src/context/search-context.ts src/context/search-provider.tsx src/hooks/use-sidebar-view.ts src/hooks/use-root-sidebar-groups.ts
bun run build
git diff --check
```

以上检查均通过。

## 6. 安全边界

本次修改修复的是前端菜单可见性与导航体验，不替代后端和路由层的权限校验。管理员接口必须继续在后端校验用户身份和权限；前端隐藏菜单只能作为展示层约束，不能作为安全授权依据。

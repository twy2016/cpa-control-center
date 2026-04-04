# Repository Guidelines

## 项目结构与模块组织

本仓库是一个基于 Wails 的桌面应用，后端使用 Go，前端使用 Vue 3。

- `main.go`、`app.go`、`platform_options_*.go`、`tray*.go`：应用入口、Wails 绑定层、平台窗口与托盘逻辑。
- `internal/backend/`：CPA 客户端、本地存储、调度器、启动器逻辑，以及对应 Go 测试文件 `*_test.go`。
- `frontend/src/`：Vue 页面、Pinia 状态、工具函数、样式和 Vitest 测试 `*.test.ts`。
- `build/`：Wails 打包资源、图标、安装器配置。
- `.github/workflows/`：CI 与发布流程。

不要提交 `build/bin/`、`frontend/dist/`、`frontend/node_modules/` 这类构建产物。

## 构建、测试与开发命令

- `wails dev`：启动桌面应用开发模式。
- `wails build -clean`：构建生产版本，产物输出到 `build/bin/`。
- `go test ./...`：运行全部 Go 测试。
- `cd frontend && npm test`：运行前端 Vitest 测试。
- `cd frontend && npm run build`：执行前端类型检查并打包。
- `bash ./scripts/build-macos.sh`：在 macOS 环境构建发布包。

建议使用 Go 1.24+、Node.js 22+，以及仓库当前使用的 Wails CLI 2.11.x/2.12.x。

## 代码风格与命名约定

- Go 代码统一使用 `gofmt`。
- Vue/TypeScript 使用 2 空格缩进、Composition API，以及现有的 `@/` 路径别名。
- 组件和页面文件使用 PascalCase，例如 `LauncherView.vue`。
- store 和工具文件使用小写命名，例如 `settings.ts`、`format.ts`。
- 优先保持函数短小、职责单一；修改行为时尽量补对应测试。

## 测试要求

- 后端测试使用 Go 标准 `testing`，测试文件与源码同目录放置。
- 前端测试使用 Vitest 与 `@vue/test-utils`，通常靠近被测模块。
- 修复缺陷时要补回归测试，尤其是调度器、启动器、额度和状态管理相关逻辑。
- 提交 PR 前至少运行 `go test ./...` 和 `cd frontend && npm test`。

## 提交与 Pull Request 规范

提交信息遵循现有 Conventional Commit 风格，例如：

- `feat(quotas): add account quota workspace`
- `fix(macos): restore native-style draggable title bar`
- `docs: refresh chinese readme`

标题应简短、直接，必要时带 scope。PR 需要说明变更目的、验证步骤、关联 issue；涉及 UI 的改动应附截图，涉及 Windows 或 macOS 的行为差异应明确说明。

## 安全与配置提示

不要提交真实的 CPA 地址、管理令牌、本地 `config.yaml`、日志文件或导出的账号数据。维护能力可能执行删除或禁用操作，相关变更必须清楚说明风险与影响。

## 最重要

- Always reply in Chinese.
- 除非用户明确要求英文，否则所有回复使用简体中文。
- 代码标识符、命令、日志、报错信息保持原始语言；其余解释用中文。
- 如果不小心用英文作答，立即用中文重写同一答案（不要只追加一句中文）

## 通用原则

- 默认使用中文回答。代码、命令、变量名、commit message 使用中文。

## Git 相关

- 除非用户明确要求，不主动提交 commit。
- 不改写历史，不强推。
- 如果建议 commit message，使用简洁风格：
    - feat: / fix: / refactor: / docs: / test: / chore:

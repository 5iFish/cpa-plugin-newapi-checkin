# Changelog / 更新记录

## v0.1.2 — 2026-11-11

- 新增 Windows ARM64 原生构建、PE 架构校验与真实 CPA 集成验证工作流。
- 发布汇总必须包含六个平台安装包及 SHA-256 校验值；缺少 Windows ARM64 时拒绝汇总。
- Add native Windows ARM64 builds, PE architecture validation, and real CPA integration workflow.
- Require all six platform archives before generating release checksums.
- Validation is in progress; completed results will be recorded before publication.

## v0.1.1 — 2026-11-10

- 支持可选站点用户 ID，为要求 `New-Api-User` 的站点补齐绑定、复查和签到请求；拒绝与令牌身份不一致的 ID。
- 增加 macOS Intel、Apple Silicon 的原生构建和 `.dylib` 安装包；Windows、Linux 双架构及 macOS 双架构均通过真实 CPA 集成验证。
- 隔离 Intel macOS 上宿主与插件之间的双向 Go 运行时调用，修复插件加载时的运行时内存崩溃。
- 校验 Mach-O 架构和动态库类型，发布汇总要求五个平台包齐全。
- 补充 macOS 安装和构建说明，说明登录与签到奖励的区别及公开站点检查的限制。

English:

- Add an optional site user ID for sites requiring `New-Api-User`; validate it against the token identity and send it for binding, rechecks, and check-ins.

- Add native macOS Intel/Apple Silicon builds and `.dylib` packages. Windows, both Linux architectures, and both macOS architectures passed real CPA integration tests.
- Isolate calls between the host and plugin Go runtimes on Intel macOS to fix runtime memory crashes during plugin loading.
- Validate Mach-O architecture/type and require all five platform archives before release checksums are collected.
- Document macOS installation/builds, authentication versus check-in rewards, and the limits of public site checks.

## v0.1.0 — 2026-11-09

首个正式版本 / First stable release.

- 支持 Windows x64、glibc Linux x86_64 和 Linux ARM64 的 CPA 原生动态库。
- 默认中文 README，提供英文切换；三个平台分别提供 ZIP 和 SHA-256 校验值。
- 多站点账号绑定：PAT / 旧版访问令牌、站点本地密码和专用刷新会话。
- 每日排程、随机偏移、按账号独立重试、手动任务与自动任务合并。
- 签到前查询状态，处理响应丢失，保留执行历史及站点累计奖励。
- 凭证和状态分别持久化；刷新恢复、删除失败及权限不足状态可见。

English:

- Native CPA libraries for Windows x64, glibc Linux x86_64, and Linux ARM64.
- Chinese-default README with an English version, platform ZIPs, and SHA-256 checksums.
- Multiple NewAPI accounts using PAT/legacy tokens, local passwords, or dedicated refresh sessions.
- Daily schedules, persisted jitter, independent retry budgets, and coalesced manual/automatic work.
- Check-before-submit behavior, uncertain-response reconciliation, history, and site-reported totals.
- Separate credential/state persistence with explicit recovery, deletion, and permission errors.

限制 / Limitations: Alpine/musl, 32-bit ARM, and unattended Turnstile/2FA challenges are unsupported. The management UI is currently Chinese. A new reward depends on the target site's policies and available check-in permissions.

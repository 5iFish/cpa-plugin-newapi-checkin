# 兼容性 / Compatibility

[简体中文 README](../README.md) · [English README](../README.en.md)

插件使用 CPA C ABI。v0.2.2 沿用下表的六平台发布工作流，在每个平台执行原生构建、单元测试、静态检查、UI、打包及真实 CPA 集成测试；全部通过后才生成完整发布包和校验值，具体运行记录见该版本的 GitHub Release。以下链接和已完成的验证记录对应 v0.1.2，不替代 v0.2.1 的发布验证。

v0.1.2 的六个平台均通过原生构建、单元测试、静态检查、UI 和打包测试，以及全部 9 项真实 CPA 集成测试，无集成测试跳过。CPA v8.0.58 宿主固定提交为 `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`。其他 CPA 版本需保持兼容的插件 ABI。

v0.2.2 retains the six-platform release workflow below: native builds, unit tests, vet, UI, packaging, and real CPA integration tests run on each platform before a complete bundle and checksums are produced. The version's GitHub Release links its verification run. The completed records linked below belong to v0.1.2 and do not substitute for v0.2.1 release verification.

All six v0.1.2 platforms passed native builds, unit tests, vet, UI and packaging tests, and all nine real CPA integration tests without skips. The CPA v8.0.58 host is pinned to `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`; other CPA versions must retain the compatible C ABI.

| Platform | Native CI environment | Actual host validation |
| --- | --- | --- |
| Windows amd64 | windows-2025; Go 1.26.10; MSYS2 UCRT64 GCC | CPA v8.0.58; 9/9 integration tests |
| Windows arm64 | windows-11-arm; Go 1.26.10 ARM64; MSYS2 CLANGARM64 GCC-compatible clang | Native ARM64 CPA v8.0.58 loading ARM64 DLL; 9/9 integration tests |
| Linux amd64 | ubuntu-24.04; Go 1.26.10; GCC | CPA v8.0.58; 9/9 integration tests |
| Linux arm64 | ubuntu-24.04-arm; Go 1.26.10; GCC | CPA v8.0.58; 9/9 integration tests |
| macOS amd64 | macos-15-intel; Go 1.26.10; Apple clang; target macOS 15 | CPA v8.0.58; 9/9 integration tests |
| macOS arm64 | macos-15; Go 1.26.10; Apple clang; target macOS 15 | CPA v8.0.58; 9/9 integration tests |

验证记录：[六平台原生 CI](https://github.com/5iFish/cpa-plugin-newapi-checkin/actions/runs/42950149076)。发布包包含同一已加载测试的动态库。macOS 默认目标为 15，不承诺更旧系统；Apple Silicon 上运行 Intel CPA 时需选 amd64 插件。Intel macOS 使用同步原生线程桥接，避免宿主与插件的 Go 运行时共享线程状态。

Evidence: [six-platform native CI](https://github.com/5iFish/cpa-plugin-newapi-checkin/actions/runs/42950149076). Packages contain the same library loaded by integration tests. macOS targets version 15; older versions are not claimed compatible. An Intel CPA process on Apple Silicon requires the amd64 plugin. Intel macOS uses synchronous native-thread calls to separate the host and plugin Go runtime thread state.

补充验证：Windows 11 x64 已通过本地 CPA v8.0.58 集成。此前版本在 Debian 12 / glibc 2.36 容器中的 CPA v8.0.52（ARM64）完成过部署验证；这不代替上表的 v0.1.2 原生 CI 记录。

Additional validation: local integration passed on Windows 11 x64 with CPA v8.0.58. An earlier build was deployed with CPA v8.0.52 on ARM64 in a Debian 12 / glibc 2.36 container; that historical deployment is separate from the v0.1.2 native CI results above.

Linux artifacts require glibc 2.34 or newer according to their imported symbols. This is a minimum symbol requirement, not a claim that every distribution at that version has been tested. Alpine/musl failed real loading with a TLS relocation error and is not supported. ARM64 does not include 32-bit ARM; Windows ARM64 is included and verified natively in v0.1.2.

Linux 产物所引用的 glibc 符号最高版本为 2.34；这是符号依赖下限，不代表已测试所有发行版。Alpine/musl 在实际加载时出现 TLS 重定位错误，未列入支持范围。ARM64 不包含 32 位 ARM，v0.1.2 已包含并原生验证 Windows ARM64。

Real CPA integration workflows validate registration, background credential callbacks, account binding, refresh persistence, session expiry, ambiguous refresh outcomes, lost check-in responses/restart recovery, deletion, and shutdown/reconfiguration. Windows symlink-specific coverage may be skipped where symlink creation is unavailable; it is exercised on Linux.

真实 CPA 集成工作流覆盖插件注册、后台凭证回调、账号绑定、刷新持久化、会话过期、刷新结果不确定、签到响应丢失后的恢复、删除与停用。Windows 无法创建符号链接时，对应测试会明确跳过；Linux 覆盖该场景。

The reference NewAPI API revision is `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`. A real older NewAPI deployment was also validated for legacy-token binding, statistics synchronization, and already-checked-in scheduled/manual tasks. A fresh check-in reward was not part of that observation because the account had already checked in. Custom site forks may differ in API scopes, expiry rules, or interactive challenges.

Login-reward mode (`checkin_mode=login`) targets sites that grant the daily reward on a fresh login. The `/api/user/self` evidence fields, the one-time state request, and the OAuth callback contract follow a public verified implementation (all-api-hub, 2026-09) for AgentRouter-style forks. On 2026-11-16 the current NewAPI contract was additionally exercised from the deployment server against `freegg.app` (NewAPI `v1.0.0-rc.49`): `POST /api/oauth/state` with `{"provider":"github","intent":"login"}` returned a `flow_token`, the GitHub authorize endpoint redirected to the site, and `GET /api/oauth/github?code=…&state=…` returned `access_token`, `session`, and `new_api_refresh`; the minted token then read `/api/user/self` successfully. That replay used the same request sequence as the plugin but was not a console bind, and the AgentRouter legacy `GET /api/oauth/state?mode=login` form has not been exercised against a real AgentRouter site from this repository. `freegg.app` also omits `last_login_time` and records its first-visit daily allowance in `setting.allowance_date`; that date is treated as the same-day reward signal. Simulated-site tests do not count as live verification. Automatic login is attempted at most once per day; an attempt marker is persisted before the callback, and an uncertain result only triggers a reward-evidence query rather than a replay. A quota rise without a login signal from the execution day is treated as an unattributed baseline change, not as today's reward.

NewAPI 接口参考版本为 `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`。

登录即签到模式（`checkin_mode=login`）针对"奖励绑定重新登录"的站点：`/api/user/self` 证据字段、一次性 state 请求与 OAuth 回调契约针对 AgentRouter 一类旧版参照公开实现（all-api-hub 2026-09 验证记录）编写。2026-11-16 另从部署服务器对 `freegg.app`（NewAPI `v1.0.0-rc.49`）实测了新版契约：`POST /api/oauth/state`（`{"provider":"github","intent":"login"}`）返回 `flow_token`，GitHub 授权端点重定向回站点，`GET /api/oauth/github?code=…&state=…` 返回 `access_token`、`session` 与 `new_api_refresh`，随后用该令牌成功读取 `/api/user/self`。该次实测与插件使用相同请求序列，但不是控制台绑定；AgentRouter 旧版 `GET /api/oauth/state?mode=login` 尚未在本仓库内对真实站点实测。`freegg.app` 不返回 `last_login_time`，而是把每日首次访问的固定补贴日期记录在 `setting.allowance_date`，插件把该日期当作当日到账信号。模拟站点测试不代表实站验证。自动登录每天最多尝试一次；回调前持久化尝试标记，结果不确定时只查询到账证据，不重放登录。没有执行日登录信号的额度上涨只作为基线更新，不冒称今日奖励。

# 兼容性 / Compatibility

[简体中文 README](../README.md) · [English README](../README.en.md)

插件使用 CPA C ABI。SDK 固定为 v8.0.58，真实加载验证覆盖 v8.0.58 和 ARM64 正式环境中的 v8.0.52。其他 CPA 版本需保持兼容的插件 ABI。

The plugin uses CPA's C ABI. Its SDK is pinned to v8.0.58; real-host validation covers v8.0.58 and v8.0.52 on ARM64. Other CPA versions must retain the compatible ABI.

| Platform | Build environment | Actual host validation |
| --- | --- | --- |
| Windows amd64 | Go 1.26.10, MinGW UCRT GCC 16.2 | Windows 11 x64, CPA v8.0.58 |
| Linux amd64 | Go 1.26.10, GCC 13.3, Ubuntu 24.04 / glibc 2.39 | CPA v8.0.58, Ubuntu/glibc |
| Linux arm64 | Go 1.26.10, GCC 11.4, Ubuntu 22.04 / glibc 2.35 | CPA v8.0.52, Debian 12 / glibc 2.36 container |

Linux artifacts require glibc 2.34 or newer according to their imported symbols. This is a minimum symbol requirement, not a claim that every distribution at that version has been tested. Alpine/musl failed real loading with a TLS relocation error and is not supported. ARM64 does not include 32-bit ARM; Windows ARM64 is not provided.

Linux 产物所引用的 glibc 符号最高版本为 2.34；这是符号依赖下限，不代表已测试所有发行版。Alpine/musl 在实际加载时出现 TLS 重定位错误，未列入支持范围。ARM64 不包含 32 位 ARM，也不提供 Windows ARM64 产物。

Real CPA integration workflows validate registration, background credential callbacks, account binding, refresh persistence, session expiry, ambiguous refresh outcomes, lost check-in responses/restart recovery, deletion, and shutdown/reconfiguration. Windows symlink-specific coverage may be skipped where symlink creation is unavailable; it is exercised on Linux.

真实 CPA 集成工作流覆盖插件注册、后台凭证回调、账号绑定、刷新持久化、会话过期、刷新结果不确定、签到响应丢失后的恢复、删除与停用。Windows 无法创建符号链接时，对应测试会明确跳过；Linux 覆盖该场景。

The reference NewAPI API revision is `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`. A real older NewAPI deployment was also validated for legacy-token binding, statistics synchronization, and already-checked-in scheduled/manual tasks. A fresh check-in reward was not part of that observation because the account had already checked in. Custom site forks may differ in API scopes, expiry rules, or interactive challenges.

NewAPI 接口参考版本为 `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`。真实旧版站点已验证旧令牌绑定、统计同步以及已签到时的自动/手动任务；该次验证前账号已经签到，未据此宣称验证了新增奖励。定制站点的权限、有效期和交互验证规则可能不同。

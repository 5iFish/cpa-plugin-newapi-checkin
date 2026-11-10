# 兼容性 / Compatibility

[简体中文 README](../README.md) · [English README](../README.en.md)

插件使用 CPA C ABI。v0.1.1 的五个平台均在原生环境中通过 CPA v8.0.58 的构建和集成测试，宿主固定提交为 `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`。其他 CPA 版本需保持兼容的插件 ABI。

The plugin uses CPA's C ABI. All five v0.1.1 platforms passed native builds and integration tests with CPA v8.0.58, pinned to commit `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`. Other CPA versions must retain the compatible ABI.

| Platform | Build environment | Actual host validation |
| --- | --- | --- |
| Windows amd64 | Go 1.26.10, MinGW UCRT GCC 16.2.0 | Windows Server 2025, CPA v8.0.58; 9/9 integration tests |
| Linux amd64 | Go 1.26.10, GCC 13.3.0, Ubuntu 24.04.5 | CPA v8.0.58; 9/9 integration tests |
| Linux arm64 | Go 1.26.10, GCC 13.3.0, Ubuntu 24.04.5 | CPA v8.0.58; 9/9 integration tests |
| macOS amd64 | Go 1.26.10, Apple clang 17.0.0, macOS 15.7.9, deployment target 15.0 | CPA v8.0.58; 9/9 integration tests |
| macOS arm64 | Go 1.26.10, Apple clang 17.0.0, macOS 15.8.2, deployment target 15.0 | CPA v8.0.58; 9/9 integration tests |

验证记录：[五平台原生 CI](https://github.com/5iFish/cpa-plugin-newapi-checkin/actions/runs/42737758242)。每个平台均执行单元测试、静态检查、UI 和打包测试，以及全部 9 项真实 CPA 集成测试；发布包使用同一已加载验证的动态库。macOS 默认以 15 为目标，不承诺更旧系统；Apple Silicon 上运行 Intel CPA 时需选 amd64 插件。Intel macOS 使用同步的原生线程桥接宿主和插件，避免两套 Go 运行时在同一线程上混用状态。

Evidence: [five-platform native CI](https://github.com/5iFish/cpa-plugin-newapi-checkin/actions/runs/42737758242). Every platform ran unit tests, vet, UI and packaging tests, and all nine real CPA integration tests. Packages contain the same library loaded by those integration tests. macOS targets version 15; older systems are not claimed compatible. An Intel CPA process on Apple Silicon needs the amd64 plugin. Intel macOS uses synchronous native-thread calls between the host and plugin to keep their Go runtimes from sharing thread-local runtime state.

补充验证：Windows 11 x64 已通过本地 CPA v8.0.58 集成。此前版本在 Debian 12 / glibc 2.36 容器中的 CPA v8.0.52（ARM64）完成过部署验证；这不代替上表的 v0.1.1 原生 CI 记录。

Additional validation: local integration passed on Windows 11 x64 with CPA v8.0.58. An earlier build was deployed with CPA v8.0.52 on ARM64 in a Debian 12 / glibc 2.36 container; that historical deployment is separate from the v0.1.1 native CI results above.

Linux artifacts require glibc 2.34 or newer according to their imported symbols. This is a minimum symbol requirement, not a claim that every distribution at that version has been tested. Alpine/musl failed real loading with a TLS relocation error and is not supported. ARM64 does not include 32-bit ARM; Windows ARM64 is not provided.

Linux 产物所引用的 glibc 符号最高版本为 2.34；这是符号依赖下限，不代表已测试所有发行版。Alpine/musl 在实际加载时出现 TLS 重定位错误，未列入支持范围。ARM64 不包含 32 位 ARM，也不提供 Windows ARM64 产物。

Real CPA integration workflows validate registration, background credential callbacks, account binding, refresh persistence, session expiry, ambiguous refresh outcomes, lost check-in responses/restart recovery, deletion, and shutdown/reconfiguration. Windows symlink-specific coverage may be skipped where symlink creation is unavailable; it is exercised on Linux.

真实 CPA 集成工作流覆盖插件注册、后台凭证回调、账号绑定、刷新持久化、会话过期、刷新结果不确定、签到响应丢失后的恢复、删除与停用。Windows 无法创建符号链接时，对应测试会明确跳过；Linux 覆盖该场景。

The reference NewAPI API revision is `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`. A real older NewAPI deployment was also validated for legacy-token binding, statistics synchronization, and already-checked-in scheduled/manual tasks. A fresh check-in reward was not part of that observation because the account had already checked in. Custom site forks may differ in API scopes, expiry rules, or interactive challenges.

NewAPI 接口参考版本为 `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`。真实旧版站点已验证旧令牌绑定、统计同步以及已签到时的自动/手动任务；该次验证前账号已经签到，未据此宣称验证了新增奖励。定制站点的权限、有效期和交互验证规则可能不同。

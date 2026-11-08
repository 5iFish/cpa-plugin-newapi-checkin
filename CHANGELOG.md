# Changelog / 更新记录

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

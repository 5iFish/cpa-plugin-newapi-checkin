# Changelog / 更新记录

## v0.2.2 — 2026-11-18

- 账号列第二行的站点网址改为链接，点击在新标签页打开（`target="_blank"` + `rel="noopener noreferrer"`）。服务端绑定时已把站点规范化为 https；前端再次校验协议，仅 http/https 生成可点击链接，损坏的状态文件无法注入其他协议地址。
- 请求状态条下方新增该账号的“可用模型”列表：读取站点 `GET /api/user/models`（当前用户组可调用的模型名，与日志读取同一凭证和 `New-Api-User` 头），以圆角标签完整展示，悬浮查看全名。
- 模型列表缓存 12 小时，并入请求状态的后台刷新任务与“刷新请求状态”按钮；页面轮询只读缓存。读取失败或接口不支持显示“数据不可确认”并保留上次列表标注过期；站点返回空列表如实显示“站点未返回可用模型”。
- 重绑和删除使该账号模型缓存失效；采集在保存前复查账号一致性与凭证代数，与请求状态相同的失效保护。仅保存模型名和时间戳，不保存凭证或站点元数据。
- `/health`、`/status` 响应新增 `models` 字段；不新增管理路由，ABI 路由数保持 11。真实 CPA 集成测试扩展覆盖模型载荷、缓存读取不访问站点与响应敏感信息清理，总数保持 10 项。

English:

- The site address on the account's second identity line is now a link that opens in a new tab (`target="_blank"` + `rel="noopener noreferrer"`). Sites are normalized to https at bind time and the console re-validates the protocol, so only http/https becomes clickable and a corrupted state file cannot inject another scheme.
- Below the request status bar, each account now lists its available models, read from the site's `GET /api/user/models` (model names enabled for the account's user group, using the same credentials and `New-Api-User` header as log reads) and rendered as chips with the full name on hover.
- The model list is cached for twelve hours and shares the request-status background worker and "刷新请求状态" button; page polling reads cache only. Failed or unsupported reads show an unconfirmed state and keep the previous list marked stale; a site returning an empty list is shown as such.
- Rebinds and deletions invalidate the account's model cache; collection rechecks account identity and credential generation before saving, with the same invalidation guards as request status. Only model names and timestamps are stored, never credentials or site metadata.
- `/health` and `/status` responses gain a `models` field; no new management routes are added and the ABI route count stays at 11. The real-CPA integration test now covers the models payload, cache-only reads, and response sanitization, keeping the total at 10 tests.

## v0.2.1 — 2026-11-17

- 新增每个站点账号的“近 24 小时请求状态”：在账号条目最下方以跨满列表宽度的 48 个时间块展示最近 24 小时，每块 30 分钟；手机端固定在该账号卡片底部。
- 状态数据来自账号自己的站点日志：`/api/log/self` 的消费记录（`type=2`）和错误记录（`type=5`）。绿色表示有消费且未读到错误，混合记录按记录成功率渐变，只有错误为红色，无请求记录为灰色；接口不支持、读取失败或超过每种日志 2000 条上限时显示未知／不完整斜纹，不伪造成功率。
- 支持鼠标悬浮、触屏点击与键盘方向键查看每个时间段的时间、消费数、错误数及记录成功率；账号列表刷新时保留当前键盘焦点。
- 账号页可见时每 5 分钟检查并后台更新缓存，工具栏提供“刷新请求状态”；页面轮询和用量统计切换只读缓存，不重复请求站点。失败的刷新保留上次结果并标记过期，重绑和删除会使该账号缓存失效。
- 日志读取不发起模型探测、不消耗模型调用额度；只保存时间范围和计数，不保存原始日志、错误正文、令牌名称或 IP。错误日志可能包含重试或漏记，因此**记录成功率不是站点可用率**。
- 后台采集按账号隔离并在分页之间释放共享凭证锁；优先处理从未检查或最久未检查的账号。插件停止或重配时取消采集，避免延迟回调。
- 新增真实 CPA 集成测试，覆盖健康接口的管理鉴权、缓存读取不访问站点、后台刷新和敏感信息不进入响应；全部平台继续要求无集成测试跳过。

English:

- Add a per-account last-24-hours request status bar below each account, spanning the full list width. It has 48 half-hour blocks on desktop and stays at the bottom of the account card on mobile.
- Aggregate the account's own site logs (`/api/log/self` type 2 consumption and type 5 errors). Show green for consumption without recorded errors, a ratio gradient for mixed records, red for errors only, and gray for no records. Unsupported, failed, or capped reads display unknown/incomplete stripes instead of a fabricated success rate.
- Support hover, touch, and keyboard access to each interval's count and success ratio, preserving keyboard focus across account-list refreshes.
- Check the cache every five minutes while the accounts page is visible and provide a manual refresh action. Polling and usage-range changes only read cached counts; failures retain the prior window marked stale, and rebinds/deletions invalidate the account cache.
- Do not send model probes or consume model quota. Persist only window and counts, never raw logs, error bodies, token names, or IP addresses. Error logs may contain retries or omit failures, so the record ratio is not uptime.
- Isolate account failures, release the shared credential lock between pages, prioritize never/least recently checked accounts, and cancel collection on plugin shutdown or reconfiguration.
- Add real-CPA integration coverage for health-route authentication, cache-only reads, background refresh, and response sanitization; all platforms must still run every integration test without skips.

## v0.2.0 — 2026-11-17

- 新增跨账号模型用量统计：24 小时 / 7 天 / 30 天区间，账户与模型 token 占比、堆叠时间图、排行、缓存率、请求数、消耗成本及平均 RPM/TPM。数据来自 `/api/log/self`，按站点 `quota_per_unit` 分别折算成本；按区间缓存聚合结果，单账号失败不影响其他账号。
- 统计图表采用内嵌 SVG，无 CDN 或第三方 JavaScript；支持悬浮提示、外侧占比标签与排行联动高亮。
- 移除被统计面板取代的单账号“用量”按钮与接口；“刷新余额”只查询钱包，不再额外翻页读取消费日志。
- 兼容新版 NewAPI OAuth：先 `POST /api/oauth/state` 获取 `flow_token`，再兼容旧版 `GET ?mode=login`；支持 `setting.allowance_date` 作为每日首次访问补贴的到账信号，修复 freegg.app 绑定失败。
- 新版 OAuth 已在部署服务器对 freegg.app（NewAPI v1.0.0-rc.49）实测完整插件登录链、身份查询、消费日志与会话刷新；不是控制台绑定验证，旧版 AgentRouter 链仍只有模拟站点测试。
- 新增账号级"登录即签到"模式（`checkin_mode=login`）：面向 AgentRouter 等把每日奖励绑定在"重新登录"事件的站点；标准接口签到行为保持不变。
- 识别站点的人机验证页：阿里云 WAF／Cloudflare 对 `/api/user/self` 返回验证码 HTML 时，单独报告 `waf_blocked`，不再误报"站点接口不符合要求"；登录模式的每日自动登录不受影响（登录链路未被拦截时照常完成，奖励金额显示未知），当日已确认的奖励不会被降级。
- 重排绑定对话框：签到模式放到绑定方式之前，只显示当前模式支持的渠道（标准签到：令牌／账号密码／站点 Cookie；登录即签到：令牌／站点 Cookie／第三方会话）；修复登录模式误禁用"高级：Cookie"、第三方会话输入区与渠道脱节、重绑时总默认到令牌页的问题。
- 统计图表跟随账号重命名：绑定改名时同步更新已缓存的统计载荷，前端再用当前账号列表覆盖显示名；过长的图表名称改为中间省略（保留首尾，悬浮标题显示全名）。
- 控制台标题栏图标替换为插件图标（内嵌 PNG data URI，不新增资源路由）。
- 登录模式用站点访问令牌检测到账（`/api/user/self` 的授予总额与最近登录时间），奖励金额按额度差推断展示，不伪造服务端日期；结果区分 待登录 / 已到账 / 金额未知。
- 可选导入第三方（GitHub/LinuxDO）会话 Cookie：保存在 CPA 凭证文件，仅发送给对应域名，插件每日用纯 HTTP 回放 OAuth 登录链自动领奖；Cookie 失效或被站点拦截时账号暂停并提示重新导入。绑定模式切回标准签到会清除已存 Cookie。
- 控制台支持模式选择、Cookie 导入、奖励状态显示与"打开站点登录"链接；登录账号不调用任何 `/api/user/checkin` 接口。
- 修复登录奖励判定：总额上涨只有在最近登录属于执行日或回调带回 `checked_in` 时才记为已到账；未归属的上涨只更新基线，不会冒称今日奖励；同日“金额未知”可在后续检测中升级为已到账。
- 修复自动登录恢复：每次登录使用独立站点会话，回调前持久化尝试标记；结果不确定时不再重放登录，只查询到账证据；手动登录会阻止同日的自动登录。
- 修复复查与重绑：复查不再吞掉未记录的奖励差值，也不会用令牌查询解除未验证的第三方会话暂停；重绑保留当日去重与到账证据。
- 修复凭证保存：省略的可选字段（如切回标准模式后的 `checkin_mode`）不再从旧文件残留；按身份重加账号会保留登录模式。
- 控制台"检测到账"改为只查询证据，不触发登录；补充授权确认/拦截状态文案。
- 修复复查失败会解除第三方会话暂停、提供商 Cookie 轮换后仍发送旧值、回调回执在后续查询失败时丢失、20 小时间隔会消耗当天任务，以及连续检测会丢失奖励历史的问题。
- 修复二次复核发现的四个问题：第三方登录暂停与奖励证据同批原子写入账号状态，崩溃恢复窗口不再丢失暂停；复查保留暂停时不再覆盖当日奖励结果（同日已到账不会被改回未知或重复记奖）；登录链路中提供商轮换后的会话 Cookie 自动写回 CPA 凭证文件，下次登录不再重放已失效的旧 Cookie；成功奖励快照与金额/历史在同一次状态更新中保存，崩溃不再丢失已确认的奖励金额。
- 控制台账号列表新增"钱包余额"列：绑定、手动复查、登录检测与每日签到任务都会顺带刷新 `/api/user/self` 的当前余额并持久化展示（未观测过显示"—"）。
- 控制台每次打开、刷新或重新加载页面后自动在后台刷新一次全部账号的钱包余额（等同点击工具栏"刷新余额"），不再显示上次任务保存的旧值；失败时保留原值且不弹提示，可手动点击按钮重试。
- 账号列表按注册（首次绑定）时间排序，重绑保留原始时间；旧记录无时间戳的排在最前。
- 新增"自动：第三方会话"绑定渠道（无站点令牌站点，如 freegg）：插件用导入的 GitHub／LinuxDO 会话 Cookie 现场完成一次站点登录，从 OAuth 回调直接获取站点会话凭证（access token + 刷新 Cookie）作为检测凭证；每日自动登录时自动轮换续期，访问令牌过期时容忍并走登录链换新。
- 报错细化：Cloudflare 等人机验证对服务器 IP 的拦截（403/挑战页）单独报告为 challenge_blocked，明确提示可在浏览器完成当天登录或配置出口代理，不再笼统显示"需人工处理"。
- 修复绑定报错误导：站点以中文提示"未提供 New-Api-User"或"New-Api-User 与登录用户不匹配"时（如 AgentRouter），之前被误报为"凭证已到期"，现在正确提示需要填写与令牌匹配的数字用户 ID。
- 发布验证纳入统计图表的渲染与交互回归测试；插件注册版本与 `VERSION` 文件必须一致。
- 修复 Windows 状态文件原子替换偶发失败：读取句柄短暂占用文件时，对访问拒绝／共享锁做最多一秒的有限重试；持续锁定仍报存储故障并保留旧文件，避免瞬时读锁误停自动任务。

## v0.1.2 — 2026-11-11

- 新增 Windows ARM64 原生构建、PE 架构校验与真实 CPA 集成验证工作流。
- 发布汇总必须包含六个平台安装包及 SHA-256 校验值；缺少 Windows ARM64 时拒绝汇总。
- Add native Windows ARM64 builds, PE architecture validation, and real CPA integration workflow.
- Require all six platform archives before generating release checksums.
- 六平台均通过全部 9 项真实 CPA 集成测试；验证记录：[native CI](https://github.com/5iFish/cpa-plugin-newapi-checkin/actions/runs/42950149076)。
- All six native platforms passed all nine real CPA integration tests without skips; see the linked evidence.

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

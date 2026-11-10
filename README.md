# NewAPI 自动签到 CPA 插件

**简体中文** | [English](README.en.md)

[下载正式版](https://github.com/5ifish/cpa-plugin-newapi-checkin/releases/latest) · [反馈问题](https://github.com/5ifish/cpa-plugin-newapi-checkin/issues) · [兼容性与验证](docs/compatibility.md)

在 CPA 管理界面添加多个 NewAPI 站点账号，按每日排程签到，查看凭证状态、执行历史与站点累计奖励。插件只声明 ManagementAPI，不代理模型请求。

## 下载

当前源码正在准备 **v0.1.1**，目标为以下五个安装包；macOS 原生构建与真实 CPA 加载仍待 CI 验证。已发布的 **v0.1.0** 提供 Windows 和 Linux 三个包，可从页面顶部“下载正式版”获取。`amd64` 即 `x86_64 / x64`，`arm64` 即 `aarch64`，不包含 32 位 ARM。

| 平台 | Release 下载文件 | ZIP 内的插件文件 |
| --- | --- | --- |
| Windows x64 | `newapi-checkin_0.1.1_windows_amd64.zip` | `newapi-checkin.dll` |
| Linux x86_64 | `newapi-checkin_0.1.1_linux_amd64.zip` | `newapi-checkin.so` |
| Linux ARM64 | `newapi-checkin_0.1.1_linux_arm64.zip` | `newapi-checkin.so` |
| macOS Intel | `newapi-checkin_0.1.1_darwin_amd64.zip` | `newapi-checkin.dylib` |
| macOS Apple Silicon（M 系列） | `newapi-checkin_0.1.1_darwin_arm64.zip` | `newapi-checkin.dylib` |

每个正式版本附带 `checksums.txt`，请从同一个 Release 下载安装包与校验文件。Linux 可运行 `sha256sum -c checksums.txt --ignore-missing`；macOS 可运行 `shasum -a 256 <安装包>`，Windows 可运行 `Get-FileHash -Algorithm SHA256 <安装包>`，对照文件内的校验值。

Linux 需要 **glibc 2.34 或更高版本**；实际验证环境和 CPA 版本见[兼容性说明](docs/compatibility.md)。Alpine/musl 不支持。请根据运行 CPA 的操作系统和 CPU 架构选择安装包；Docker 部署以容器运行环境为准。

macOS 构建以 **macOS 15** 为默认目标。Intel 和 Apple Silicon 使用独立原生包；按 CPA 进程架构选择，Apple Silicon 上通过 Rosetta 运行的 Intel CPA 应匹配 `darwin_amd64`。Mac 上的 Linux 容器仍使用 Linux 包。

## 安装

本项目使用 CPA C ABI 动态库，不是 Go 的 `plugin` 包。Windows 加载 `.dll`，glibc Linux 加载 `.so`，macOS 加载 `.dylib`。源码依赖锁定 **CPA v8.0.58**，提交 `e5e6ad1e1b91fc20ab9964e6fb22c0c269b9d882`。NewAPI 接口对照提交 `a3fd673bb7897691b1041aaf8d78bab6cc15e1d5`；定制版和旧版需要满足相同接口契约。

1. 解压安装包，将动态库放进 CPA 插件目录内对应的平台子目录：`plugins/windows/amd64/newapi-checkin.dll`、`plugins/linux/amd64/newapi-checkin.so`、`plugins/linux/arm64/newapi-checkin.so`、`plugins/darwin/amd64/newapi-checkin.dylib` 或 `plugins/darwin/arm64/newapi-checkin.dylib`。
2. 在 CPA 配置中启用插件。以下是固定版本支持的配置结构；已有服务只需合并 `plugins` 部分，不必覆盖现有管理密钥。

```yaml
management:
  secret-key: "请设置自己的管理密钥"
plugins:
  enabled: true
  dir: "plugins"
  configs:
    newapi-checkin:
      enabled: true
      checkin-at: "09:00"
      jitter-minutes: 20
      timezone: "Asia/Shanghai"
```

3. 启动 CPA，在插件菜单打开 **NewAPI 签到**，或访问 `/v0/resource/plugins/newapi-checkin/console`。
4. 输入 CPA 管理密钥并添加账号。页面将密钥保存在当前标签页的 `sessionStorage` 中；密钥不会进入 URL。管理鉴权失败后立即停止轮询，重新输入正确密钥后才能恢复。

页面静态资源可以公开访问，账号和任务数据必须通过受 CPA 鉴权保护的 `/v0/management/plugins/newapi-checkin/…` API 获取。请为远程管理部署 HTTPS。

## 选择绑定方式

| 方式 | 条件与限制 |
|---|---|
| PAT / 访问令牌 | 首选。需 `profile:read`、`wallet:read`、`wallet:write` 三项权限。站点创建 PAT 默认 30 天；不设到期需在站点显式选择 **Never expires**。插件中的到期日期只作提醒，留空表示未知。旧版 legacy 令牌可能受站点退役时间限制。 |
| 站点账号密码 | 仅用于站点本地密码；不收集 LinuxDO 密码。不保存密码，创建独立登录会话；使用站点提供的 v2 密码加密协议。2FA/passkey 等交互验证只提示，不代办。 |
| 专用 refresh cookie | 高级导入。需新建专用登录会话并明确确认，之后不要在浏览器/其他工具继续使用该会话。已管理或刷新结果不确定的会话不能重复导入。 |

部分 NewAPI 站点（如 `new.xkool.cfd`）还要求 `New-Api-User` 请求头。使用访问令牌绑定时，请在“站点用户 ID”中填写同一账号的数字 ID；可从已登录网页的 `/api/user/self` 请求头查看。不要求此头的站点可留空。插件会校验该 ID 与令牌实际身份一致，并在复查、自动签到和重新绑定时复用已验证的 ID。管理 API 对应可选字段为 `upstream_user_id`。

登录会话最多 30 天，刷新 access token **不会延长原会话到期时间**。会话到期、撤销或旧令牌退役时，需要重新绑定。同站点同用户重绑保留历史，取消旧任务并重新检查。

站点若对签到启用了 Turnstile，插件会保存账号并标记暂停，无法无人值守签到；更换凭证类型不能绕过验证。HTTP 只允许 loopback 测试地址，其他站点必须 HTTPS，正常校验证书且不跟随重定向。

绑定操作为“验证并保存”，会查询身份与签到状态，不会为了测试写权限偷偷签到。`wallet:write` 是否可用要到首次实际签到时才能确认。

## 登录与签到

登录方式与领取奖励的任务是两件事。NewAPI 上游的 `/api/user/login` 建立登录会话；签到奖励由独立的 `POST /api/user/checkin` 发放。插件通过 PAT 或已保存的会话执行签到，不需要每天重新输入密码。

以 `https://new.xkool.cfd` 为例：2026-11-09 的公开 `/api/status` 返回 `checkin_enabled=true`、`turnstile_check=false`，FAQ 写明“每天签到 1–5 中转币”。因此应使用现有签到流程。该站点未公开版本，以上公开检查不能证明是否额外定制了登录奖励，也不代表已登录或实际领取奖励。

添加时填写站点根地址，使用该站点的个人访问令牌；不要填写模型调用用的 API Key 或 CPA 管理密钥。绑定成功后，可手动签到或等待排程。当前没有账号级“仅登录”模式；单纯保存凭证或刷新令牌不能被记录为签到成功。首次绑定只验证并保存；后续自动任务可能按排程执行，重新绑定现有账号也会安排补查和必要的签到。

## 排程、重试与统计

- 默认每日 09:00、随机偏移 ±20 分钟；可选 0–120 分钟。空时区使用宿主本地时区，也支持 IANA 时区名称。页面覆盖值优先于宿主配置，恢复默认会清除覆盖值。
- 每日随机时间持久化；重启后已到期且未入队的任务分散在 2–5 分钟补查。账号间隔 1–3 秒。
- 每个账号工作项最多 3 次业务尝试，重试间隔基数 30 / 120 秒，加 0–10 秒随机偏移。遵守 `Retry-After`。同账号的手动与自动请求合并，其他账号的重试预算互不影响。
- 次日计划接管前一天尚未结束的重试时，使用新的每日预算，并保留旧任务的重试时间与待确认标记；正在执行的任务先完成，不并发签到。
- 执行前查询 `/api/user/checkin`，已签到则不再提交。POST 结果不确定时先查询，再判断是否重试；最终防重复依赖站点唯一约束，不提供跨系统 exactly-once 承诺。
- 不补交昨天的签到。记录使用站点返回日期；日期未知时不伪造。推荐选择远离站点午夜的执行时间。
- “站点累计签到奖励/次数”来自站点 `total_quota/total_checkins`，包含插件之外的签到，使用原始 quota，不跨站点相加或转换为货币。统计刷新失败保留旧值并显示待更新，已完成签到不会因此重新 POST。

## 持久化和故障处理

必须分别持久化：

- **CPA auth 目录**：保存 `newapi-checkin-<完整账号哈希>.json` 物理凭证文件，包含令牌/refresh cookie，按秘密文件管理。
- **插件状态目录**：默认为 `~/.newapi-checkin-plugin`，可用 `NEWAPI_CHECKIN_PLUGIN_HOME` 覆盖，保存 `state.json`。它包含设置、账号摘要、最近 30 天历史、任务和恢复标记，不保存令牌、cookie、密码或管理密钥。

仅支持一个 CPA 进程拥有一份凭证/状态目录，不支持多个实例共享旋转会话。

刷新前持久化待确认标记，保存并读回新凭证后才继续业务。如果上游刷新成功但本地保存失败，只重试本地保存；响应丢失或重启只能找到旧代凭证时停止使用旧 cookie，要求新会话重新绑定。状态文件损坏不会自动重置覆盖，应停止插件并保留文件，修复文件或从备份恢复后重启。

运行时状态写入失败会停止自动任务，并在管理页显示 `storage_error`；旧的账号摘要仍可读取。修复磁盘空间、目录权限或文件冲突后重启插件，让任务恢复过程重新确认状态。

删除入口只删除一个经过身份/所属校验的凭证文件，并持久化删除标记；保留历史，防止宿主旧索引把账号重新导入。删除失败可再次尝试。

删除失败的账号仍显示在列表中，自动任务保持暂停，确认文件已不存在后才隐藏。物理凭证文件丢失时，可以在原账号上重新绑定；已有损坏或归属不明的文件不会被重绑直接覆盖。已确认权限不足的凭证不会因 GET 重新检查成功就恢复自动签到，需要使用具备相应权限的新凭证重新绑定。

**从 CPA 面板或外部程序修改凭证的限制：**刷新前及保存前均检查文件、代数和禁用状态；已观察到的修改会停止后续保存。CPA 的 `host.auth.save` 没有比较后写入的事务保证，最终检查与写入之间仍有竞争窗口。需要保证删除期间不会被在途保存重建时，请使用本插件删除入口，或先停止插件并等待任务退出后再外部操作。

## 从源码构建与验证

需要 **Go 1.26.10 或更高版本**和目标平台的 C 编译器。模块依赖在 `go.mod/go.sum` 锁定，构建脚本禁用开发机 `go.work`。

Windows PowerShell：

```powershell
$env:PATH = "C:\path\to\go\bin;C:\path\to\mingw\bin;" + $env:PATH
./scripts/build.ps1
```

Linux：

```sh
sh scripts/build.sh
```

macOS（安装 Xcode Command Line Tools，使用 clang）：

```sh
sh scripts/build-darwin.sh
```

Linux/macOS 脚本自动识别本机 `amd64` / `arm64`，分别输出到 `dist/linux-<架构>/`、`dist/darwin-<架构>/`；Windows 输出到 `dist/windows-amd64/`。脚本拒绝与本机不匹配的 `GOOS/GOARCH`。macOS 默认 `MACOSX_DEPLOYMENT_TARGET=15.0`；调整该值不代表已验证其他系统版本。构建发布包还需要 Python 3（macOS/Linux 通常使用 `python3`）：

```sh
python scripts/package-release.py --platform linux-amd64
# 另可选择 linux-arm64、windows-amd64、darwin-amd64 或 darwin-arm64
```

自动化测试：

```sh
GOWORK=off CGO_ENABLED=1 go test -p=2 ./... -count=1
node --test tests/ui/console.test.mjs
python3 -m unittest discover -s tests/packaging -v
GOWORK=off go vet ./...
GOWORK=off CGO_ENABLED=1 go test -race -p=2 ./internal/core ./internal/credential ./internal/account ./internal/config ./internal/engine ./internal/app
```

真实 CPA 集成测试使用 `CPA_TEST_BIN` 指定由上述固定提交构建的宿主；测试生成独立配置、auth、state 目录，使用本地可控 NewAPI 服务。没有设置该变量时明确跳过，不算真实加载通过。

```sh
CPA_TEST_BIN=/absolute/path/to/cpa GOWORK=off CGO_ENABLED=1 go test -p=2 -tags=integration ./tests/integration -count=1 -v
```

验证已构建的交付文件时，再设置 `CPA_TEST_PLUGIN=/absolute/path/to/newapi-checkin.so`（Windows 使用 `.dll`，macOS 使用 `.dylib`）。工作流测试将复制并实际加载该文件；ABI 探针仍单独构建，不混入产品动态库。

测试目录和日志保留，路径打印在测试输出中。版本信息见 [CHANGELOG](CHANGELOG.md)，平台验证见[兼容性说明](docs/compatibility.md)。发布包命名与 CPA 插件安装器匹配；GitHub Release 发布和官方插件商店收录是两个步骤，本仓库不代表已获官方商店收录。

## 许可证

[MIT](LICENSE)。随包第三方组件的许可见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md)。

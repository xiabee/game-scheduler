# Security Policy / 安全说明

## Threat model（威胁模型）

game-scheduler 是一个**本地编排器**:它按配置启动你已安装的外部自动化工具,并记录结果。理解以下边界后再决定如何部署:

1. **API 即命令执行。** 任务的 `raw_args` / `exe` 字段(以及游戏的 `tool_path`)由设计决定了"能调 API 的人就能在本机以服务进程权限运行任意命令"。这是本工具的核心用途,不是漏洞——因此:
   - 默认只绑定 `127.0.0.1`,**不要**在未加 `auth_token` 的情况下绑定到其它地址;
   - `auth_token` 是单一共享密钥,适合个人/可信局域网;对外网或多用户场景,请置于做 TLS + 真实认证的反向代理之后;
   - 任何拿到 token 的人 = 任何能在这台机器上执行命令的人。请像对待 SSH 私钥一样对待它。
2. **钩子命令由操作员配置。** `screenshot_cmd` / `notify_cmd` 是你自己写的 shell 命令模板;动态字段(`{{.Title}}` 等)在替换前会**剥离 shell 元字符**,防止工具输出注入。但模板本身就是命令——只填你信任的内容。
3. **外部进程不被沙箱化。** 调度器以普通子进程方式运行外部工具,不注入、不读内存、不抓包,也**不**限制这些工具自身的行为。工具本身的安全性/合规性由其项目与使用者负责。
4. **出站网络。** 服务器本体唯一的出站请求是(可选的)B 站公开搜索 `api.bilibili.com`(只读、无凭据)。除此之外不回传任何遥测。
5. **数据落盘。** SQLite 数据库、执行日志(含 stdout/stderr)、失败截图都明文存放在 `data_dir`。日志里可能包含工具打印的账号昵称等信息,分享前自查。

## Hardening checklist（部署加固清单）

- [ ] 保持默认 `127.0.0.1` 绑定,或设置强随机 `auth_token`(`GS_AUTH_TOKEN`)
- [ ] 不要把 `config.json`(可能含 token)提交进任何仓库——本仓库 `.gitignore` 已排除
- [ ] 反向代理场景:启用 TLS,并在代理层做认证/限流
- [ ] 定期查看 Security workflow(govulncheck 每周自动跑)与 Dependabot PR

## Reporting a vulnerability（漏洞报告）

发现安全问题请**不要**开公开 issue,直接通过 GitHub 的
[Private vulnerability reporting](../../security/advisories/new) 提交,
或联系仓库所有者。会尽快确认并修复。

## Scope notes（范围说明）

以下**不视为**本项目漏洞:
- 利用已授权的 API(含合法 token)执行命令——这是设计行为(见威胁模型 #1);
- 外部自动化工具(BetterGI / March7thAssistant / Fhoe-Rail / ok-ww / M9A)自身的问题——请报给对应项目;
- 因违反游戏服务条款导致的封号等后果——见 README 免责声明。

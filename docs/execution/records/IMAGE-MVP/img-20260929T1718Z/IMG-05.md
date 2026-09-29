# IMG-05 · 登记目录与固定 Digest

输入提交 `1eb486ac17e9f50471b296761ab07c5e1b2b5487`；Fedora 格式化白名单返回后，最终代码提交 `b0fb48cecd843932928a7b9dbc9f9c054c3d7a2f`。有效门禁为 [budgeted 日志](IMG-05/b0fb48cecd843932928a7b9dbc9f9c054c3d7a2f-verify-budgeted/output.txt)，SHA256 `77923b9578cd9a1f97acda846017b905fb67768c209a3e96576b533688622226`，exit 0，2026-09-29T18:45:11Z—18:47:40Z。

Fedora cwd 为 run 下 `repo/resource/<完整SHA>`；使用既有 `net05a-heavy.lock`，systemd scope `run-p713129-i9098991.scope`，CPU 200%、MemoryMax 2300M、MemorySwapMax 0；Go/TMPDIR/单个 PG 预算同 runner。命令依次为 `make generate` 后 clean 检查、Image 定向 race、`scripts/image-integration -v -race`、真实 PG 两项租户 mutation、`make verify` 及旧 Network 保护路径 diff。所有门禁通过；mutation 的预期断言失败由 runner 验证，不是静默忽略失败。

| Task | 实现和证据 |
|---|---|
| IMG-05.1 | `Catalog.RegisterImage`：先限定 registry/project，完成 command 先重放；真 PG 中登记 A 后 tag 移到 B，同键仍返回 A 且不再调用 provider |
| IMG-05.2 | `GetImage/readRegistration`：tenant/platform 分开查询，跨租户404，平台只读，自己的取消记录仍可读 |
| IMG-05.3 | `CursorCodec/ListImages`：HMAC 游标绑定租户/scope/筛选/limit；真 PG 的 literal `100%` + 用途筛选在 LIMIT 前，两页2+1条无重复；篡改和跨绑定拒绝 |
| IMG-05.4 | `UpdateImage`：四类元数据、版本CAS、digest/ref不变；过期版本与平台写入反例 |
| IMG-05.5 | `UnregisterImage`：取消无额外 registry 读写，同内容重新登记新 ID；已取消记录不能运行解析 |
| IMG-05.6 | `Runtime.ResolveImageForWorkload/GetTenantPullMaterial`：按固定digest查询、目标平台匹配、503保留记录；租户/服务身份隔离、过期pull拒绝，platform结果仍返回请求租户 |

`TestCatalogUseCasesAndRuntimeIsolation` 使用真实 PostgreSQL 与受控 Registry port；Harbor TLS 协议测试和既有进程退出/并发测试同时通过。不替代已批准 Harbor 或普通容器 owner 的真实联调；live仍 blocked，product本批n_a。

执行偏差：首次 `-verify` 调用遗漏外层 flock/systemd 限制，发现后停止本任务 runner PID 707997 和直接子进程 make PID 709988；外层 SSH exit 255，内部 EXIT trap 在信号中打印的 exit=0 不作为通过证据。该轮原始日志保留，结果排除。检查该 SHA cwd 已无进程，临时 PG 已清理后，以上述完整受限命令重跑全部门禁。有效轮 PG `58712212e277bd10835f756613697e2ffcad7084fd1f00f071a83b54c8c6662f`、`7989a4b351cc85c1905a2bc6dac2ded049e8f17e25901ab9eb8529578a1cc20e` 均按 ownership 清理；最终 docker ps 空。未操作共享 Harbor/集群。

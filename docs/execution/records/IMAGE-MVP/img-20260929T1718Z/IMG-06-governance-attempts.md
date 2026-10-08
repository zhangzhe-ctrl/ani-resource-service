# IMG-06 Governance 生成诊断记录

此文件记录尝试与恢复，不表示门禁已通过；当前任务状态只在 Resource `docs/execution/status.md`。

Governance 隔离工作树 `/home/chabking/workspace/.worktrees/governance-image-mvp-20260930`，review branch `codex/image-mvp-20260930`。原工作树未改动。Resource 固定契约为 `71aa986078dfb64388d783a7b2de01b7a94b0027`，Go module 解析为 `v0.0.0-20260929185545-71aa986078df`，无 local replace。以下均在 Fedora 本 run 中执行，未使用 live profile，也未触及共享 Harbor/集群。

生成中的真实失败与处置：

- 早期 `89a9f40` 使用默认代理时 sumdb 请求失败；只在本轮进程设置官方 GOPROXY/GOSUMDB，未关闭校验或修改全局配置。
- 同一版本尝试将既有 Module proto 加入 STANDARD lint，既有未加前缀枚举及 import 触发错误。恢复 Image 两份 proto 原有严格 lint 范围；Module 仍参与 Buf build/generate，并增加固定既有名称/编号及 IMAGE=14 的测试，不建立全局 lint 例外。
- `2192db7` 误用 `go run ./tools/localdeps/gow`（目录无 main）失败。后续改为仓库 `make gow`。
- `2eb43c9d929dd96a5c9fe1fea9bd9927101a0a0d` 首次 Ent 冷编译触发工具两分钟超时，子进程仍在本轮 scope。只停止已核对命令和目录的 `run-p789655-i9187579.scope`；SSH exit 141，确认 scope inactive，无残留本 SHA 进程。记录目录 `gov-2eb43c9d929dd96a5c9fe1fea9bd9927101a0a0d-generate` 不作为成功证据。
- 同一 SHA 新隔离目录 `-warm` 先预热 schema 编译，随后 Ent 仍 exit 1。直接诊断得到 `use of internal package .../internal/data/ent/schema not allowed`，因为工具从 repo root 建立临时 loader。未修改工具；使用仓库已有 `make -C app/admin/service ent`，保留五项固定 feature。诊断输出保留 `ent-diagnosis.txt`。
- 同一 SHA 新隔离目录 `-service-cwd`，生成 exit 0，但审阅发现暂存 OpenAPI 缺少既有 AK 创建 201 后处理，因此未导入该轮输出。
- 源码 `f2818ae3a788a1452fe329ab23af81ad96815a3c` 补上在暂存目录运行现有 `finalize-aksk-openapi.py`，重新生成 exit 0，2026-09-29T19:41:12Z—19:43:55Z，scope `run-p795397-i9177674.scope`。返回25文件，每一项均校验源 SHA、白名单、base SHA256 和返回 SHA256 后原样导入；只追加 Module/Ent IMAGE 枚举及 Image OpenAPI，Go module 只新增固定 Resource 契约，go.sum 为 tidy 的实际输出。

导入提交 `9690a811d257d2ff88e835bc064e1affb3bb126d` 已推送；新 SHA 门禁另行执行，不将生成成功当测试通过。各次均使用既有 flock、CPUQuota=200%、MemoryMax=2300M、MemorySwapMax=0，task-owned caches 与短 TMPDIR `/home/chabking/.im-1718`。旧失败目录保留，不 reset 或覆盖。

新增 `scripts/image-joint-integration` 只创建本 run 的私有 PG/Redis、随机密钥、loopback 端口和受限 runtime role，按容器 ID+ownership 清理。其下游是实际 mTLS 协议 peer；只证明 Governance HTTP/JWT/AK/Casbin/租户映射/响应边界，不构成实际 Harbor、Resource 持久化或产品验收。

`9690a811d257d2ff88e835bc064e1affb3bb126d` 新 SHA 重生成 clean；imagecontract/sql-bootstrap/auth/logging/constants/rpc 和 data 包测试 pass。Service 包的既有 `TestTaskService_RealAsynqSchedulerLifecycle` 因未提供 `ANI_TEST_REDIS_URI` fail，Server 包 pass；因此整轮 exit 1，race/后续 build 尚未执行。单独执行同 SHA 的 Image HTTP fixture 时 PG16.10 与 Redis7.4.6 启动、7个原迁移通过，随后测试因未显式注册 database/sql 的 pgx driver fail（未到业务请求）。本轮 PG `ae34f7edd34109823109846adcda272ffbd9fbc9bd7395d7131964d7a51ab07b`、Redis `cb22bcb64679072748d01cc3e07fdab345846fc826a6606ae06ab6bc06cd9a1e` 都按 ownership 清理。

审阅生成 OpenAPI 发现路径参数被 gnostic 转成 `{imageId}`，而 Kratos HTTP route/权限 catalog 使用 `{image_id}`。源码按现有 VPC 契约的方式显式固定 Image ID 字段 `json_name="image_id"`，没有改全站编码器；新增全部11路由的 OpenAPI/权限映射一致性测试，并用真实 HTTP 校验 `image_id` 和 int64 JSON。测试装配补 pgx stdlib 注册；隔离 fixture 增加 `--regressions`，提供带密码的自有 Redis URI 给原 Asynq 回归，不 skip 或删除旧测试。

修正源码 `ac36068042d3714a612c408b63b6c46e3bdb4c5e` 生成 exit 0（2026-09-29T19:55:11Z—19:59:37Z），返回4文件，依白名单/base hash 导入提交 `d64d6ee478801795afadb4573ba25f8c2de6b7fc`，已推送。后续 runner 加 `-trimpath`，以免独立 SHA 目录的绝对路径导致每轮重编相同依赖；CPU/内存/swap 并发预算不变。新 SHA 的验证单独执行，前序局部结果不代替它。

`d64d6ee478801795afadb4573ba25f8c2de6b7fc` 首轮 verify（2026-09-29T20:00:27Z—20:08:55Z，scope `run-p810982-i9200661.scope`）重生成 clean、基础合同/鉴权/日志测试通过，真实私有 Redis 上的完整 data/service/server 回归通过，含原 Asynq lifecycle；随后 race 编译触发 systemd `Result=oom-kill`，内存峰值2.2G，SSH exit 141。该轮没有总门禁通过，不能以 scope 终止前的局部 pass 代替。两个 fixture `c1f877877f8715968a5aa68b139f8ba828a73982475d84871e909222b3e05a9e`（PG）、`cdfd301a82a864f332e360767493e0cdb90565c70e3e8fa392fc5300d40da41c`（Redis）均在 race 前按 ownership 清理；scope 已失败终止，无残留本 SHA 编译进程。

恢复采用同 SHA 的新目录 `-serial`，沿用原 flock 和 CPUQuota=200%/MemoryMax=2300M/MemorySwapMax=0，仅把 Go 编译并发降为 `-p=1`、GOMEMLIMIT=768MiB、GOGC=50。2026-09-29T20:09:43Z 启动 scope `run-p826002-i9220950.scope`，完整重跑生成、回归、race、build、HTTP 联调，未跳过门禁或增加预算。源码没有因资源失败而修改；结果单独归档。

`-serial` 在2026-09-29T20:14:28Z再次由OOM终止，SSH exit141；完整原有回归已通过，fixture清理后才进入race。随后 `gov06-remaining.sh` 同SHA先完成默认优化的server/admin build和真实HTTP联合测试（0.69s），再以GOMAXPROCS=1、GOMEMLIMIT=512MiB、GOGC=20尝试race；scope `run-p829459-i9218579.scope` 于20:23:26Z由OOM终止，SSH exit141。两个失败都是编译资源失败，没有断言失败或race报告。

最后仅对生成Ent包设置 `-gcflags=go-wind-admin/app/admin/service/internal/data/ent=-l`，保留所有race插桩和断言，仍为原2300M/无swap预算。2026-09-29T20:24:07Z—20:32:27Z，scope `run-p830669-i9206543.scope`：auth/data/service Image定向race、真PG/Redis HTTP/mTLS race全部pass，整轮exit0。默认优化race仍属未完成，不能用该参数的成功覆盖三轮OOM。实际命令、hash与资源清理见[结果记录](IMG-06-governance.md)。

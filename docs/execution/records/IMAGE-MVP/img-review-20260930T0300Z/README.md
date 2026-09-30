# Image MVP：R1—R3 审核修复

本轮 R1—R3 修复及要求的有界回归已完成，交付 review 分支等待用户审核/手动合并。没有推进 IMG-08 普通容器产品接入、IMG-09 前端或共享产品部署。唯一当前状态见 [status](../../../status.md#image-mvp)；旧 [HANDOFF](../img-20260929T1718Z/HANDOFF.md) 保留为历史事实。

## 基线与候选

| 仓库 | 审核基线 | 本轮运行源码 SHA |
|---|---|---|
| Resource | `8f317deef04d034e7b7e0459aacb25a70f01360c` | `fa56b55dbbc892bc40586a8303bff54826764e7e` |
| Governance | `d64d6ee478801795afadb4573ba25f8c2de6b7fc` | `e608cba9bf7c5ccfdb3dad41471525ff541c1ad1` |

两仓均在 `codex/image-mvp-20260930` 追加普通提交并推送。开始时目标工作树干净，HEAD 等于审核基线，没有需保留的后续修复提交。Governance 原工作树已有未跟踪 `_agent/` 保留；本轮仅操作其现有独立 review 工作树。收尾时 Resource 工作树另出现其他任务的 `img-cleanup-20260930T0344Z/` 未跟踪目录和 status 尾部清理补充；本轮没有修改或纳入提交。没有操作前端仓库、回退、改写提交或合并 main。原 API 生成契约未变化，Governance 固定 module 契约仍为 `71aa986078dfb64388d783a7b2de01b7a94b0027`；跨仓执行用精确 Resource 测试二进制，不使用 local replace 或跨仓 internal import。

## R1：确定拒绝可用原 key 重试

结论：审核路径成立；并额外复现“POST 已成功但 ID 尚未落库，后续 GET404 导致第二次 POST”。

- [Harbor adapter](../../../../../internal/data/image/harbor_project.go) 只将完整 400/401/403 归类为明确拒绝；HTTP 状态证据保留在适配层，向领域传递用途限定标记。依据是 Harbor v2.15.2 [Project handler](https://github.com/goharbor/harbor/blob/v2.15.2/src/server/v2.0/handler/project.go) 的创建前认证、授权、输入校验，以及 [controller](https://github.com/goharbor/harbor/blob/v2.15.2/src/controller/project/controller.go) 的事务边界。本请求不创建 proxy-cache Project。
- [Lifecycle](../../../../../internal/biz/image/lifecycle.go) 将明确拒绝持久化为 `retryable/project_rejected`，保留原 command ID/key；再请求时仍先核对绑定、同名对象、actor/参数和重放结果。没有删 command、改唯一约束或换 key 解锁。
- 超时、断链、5xx、409、截断拒绝响应、成功响应缺 ID、绑定提交前失败均保留未知结果；已发送阶段不能因单次404重发。未知同名 Project 不自动认领；有实际 ID 时继续受控归属恢复。
- 历史 `blocked/SpaceOwnershipUnconfirmed` 没有保存“确定拒绝”证据，不能用当前404自动改判。这一安全边界不因本轮升级而放松。

[定向测试](../../../../../internal/data/image/project_retry_integration_test.go) 走真实 PG、Lifecycle、Harbor TLS/HTTP 适配器与隔离 provider 故障夹具。断言 3 种明确拒绝零创建、修正后原 command 成功且最终仅一个 Project；未知结果不重发、显式恢复成功；未知同名对象、正常重放、actor/参数冲突。外部计数指真实夹具 HTTP/副作用计数，不冒称 live Harbor。

## R2：同一持锁 session 执行短 SQL

结论：审核路径成立。旧实现的屏障让 8 个不同空间均取得 session advisory lock 后再执行 SQL，8 个全部超时，成功数和外部写数均为0。

[Postgres.connection](../../../../../internal/data/image/postgres.go) 与 [WithSpaceWriteLock](../../../../../internal/data/image/commands.go) 将当前 Postgres 实例的持锁连接通过私有 context 传给顺序回调；Image repository 的短查询/短事务复用该连接。连接池仍为8，无新增锁池。上下文不跨实例使用，不允许嵌套同实例空间锁；保持跨进程 session advisory lock、5秒等待上限、取消和异常连接丢弃、独立清理解锁上下文。

[真实 PG 测试](../../../../../internal/data/image/space_lock_integration_test.go) 在同样屏障下要求8/8成功、Project/Robot/Secret各8次，重放无新增；检查无残留锁、总业务连接不超过8、Close后连接归零。同时覆盖同空间竞争、两个真实进程、请求取消、回调失败、终止自有锁连接后的正常请求。在每次真实 Harbor HTTP 夹具请求中用独立 PG observer 检查 `xact_start`，证明没有 SQL 事务跨越外部调用。

## R3：未签发停用为稳定业务冲突

结论：两仓真实边界均复现。旧 Resource 实际 gRPC 返回 OK 并保存 command，Governance 实际 HTTP 返回503。

[BeginTenantCredentialCommand / CompleteTenantDisable](../../../../../internal/data/image/lifecycle.go) 增加 `CREDENTIAL_NOT_ISSUED`：新请求先检查已有 command 重放/冲突，再检查 CAS，generation=0 不预留 command；完成阶段也拒绝 generation=0。Resource [gRPC](../../../../../internal/service/image/errors.go) 返回 FailedPrecondition，Governance `image_views.go:202` 保留 ErrorInfo reason，映射HTTP409。BFF 成功响应仍必须是 disabled，未放松校验。

[Resource 合同测试](../../../../../internal/data/image/credential_contract_integration_test.go) 使用真实 PG、TenantService、GovernanceTLS/GovernanceUnary 和 mTLS 客户端；覆盖未签发、不落 command、active、disabled、旧版本、同 key 重放/actor/参数冲突、重新签发后的完成结果重放，以及未完成旧 command 的完成拒绝。

Governance `image_joint_http_test.go:239` 复用真实 PG/Redis/JWT/AK/Casbin/租户映射/HTTP，启动精确 Resource SHA 编译的测试进程进行上述停用链路。Harbor 仍为 provider fixture，非产品 live。helper 仅在 Resource `_test.go` 中存在，无生产测试后门。两仓规格已更新；Governance 合同入口为 `docs/contracts/image-publisher-disable.md`。

## 执行与证据

运行位置 `/home/chabking/ani-image-mvp-runs/img-review-20260930T0300Z`（Fedora）。本地只读写源码/文档、Git 和转运。沿用锁 `/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock`，没有 Ubuntu/本地重任务兜底。

工具：Go `go1.26.7-X:nodwarf5 linux/amd64`，Buf1.60.0，sqlc1.31.1，Python3.14.7；Resource PG18.6，Governance PG16.10/Redis7.4.6，均为既有脚本固定 image digest。`TMPDIR=/home/chabking/.ir-0930`；复用先前同任务专属 `img-20260929T1718Z/cache/{go-build,go-mod,gopath,tools}`，不改全局环境或安装工具。

重任务串行，总 supervisor 时限5400秒；单门禁1200秒，旧 Network integration/race各1500秒（原测试超时20分钟）。无容器阶段CPU200%/Memory2300M；Resource/Network PG阶段主进程CPU100%/Memory1532M + PG1CPU/768MiB；Governance主进程CPU50%/Memory1404M + PG1CPU/768MiB + Redis0.5CPU/128MiB。总计不超过2CPU/2300MiB，所有swap=0。

所有生成轮次基于已推送精确 SHA，在Fedora完成 Buf/sqlc/gofmt；生成返回manifest的files均为空，无需覆盖本地源码。最终文档提交与测试源码的关系由运行源码一致性清单证明，不把旧 SHA 回归直接记到改后源码。

### 定向 red / green

| 场景 | red 源码与实际退出码 | 观察 |
|---|---|---|
| R1 明确拒绝 | `eb65a98e64ea4dead869e78d4800fbf6c08b8c6d` / 1 | 400/401/403 均永久 blocked/project_sent |
| R1 绑定未提交 | `ad3f6d3caf3bedb169ade38ad99fb64a8e7a4b60` / 1 | 隐藏同名查询后，POST计数达到2 |
| R2 8空间屏障 | `46e8315e835c2d88d9a8dc9a844462731927803a` / 1 | entered=8、succeeded=0、外部写=0 |
| R3 Resource真实RPC | `71cae63cb0af01332d38d7a9ef590b9482954ac3` / 1 | not-issued 返回OK并落command |
| R3 Governance真实HTTP | Governance `61096fac8661cb9037c0b14794f9b7fd7c0a2b12` + 上行Resource SHA / 1 | 请求实际503，而约定409 |

另保留 Resource `ec863ea460a7b52f98b53538ae73a7779511bcd3` 首次测试夹具错误：过短的幂等键使 Ensure 被 InvalidArgument 拒绝，exit1；这一轮不是R3复现证据。修正夹具后才获得上表的 red。没有改写失败日志。

最终 Resource `fa56b55dbbc892bc40586a8303bff54826764e7e` 的 R1/R2/R3定向重跑均exit0；对应目录为 `evidence/final-r1-<SHA>`、`final-r2-<SHA>`、`final-r3-<SHA>`。Governance `e608cba9bf7c5ccfdb3dad41471525ff541c1ad1` + 同一最终 Resource SHA 的真实HTTP也exit0，未签发409且reason断言通过。

### 最终候选门禁

下表命令在上述各仓精确 SHA 目录执行。`TOOLS=/home/chabking/ani-image-mvp-runs/img-20260929T1718Z/cache/tools`。完整环境、命令和预算见 [Resource runner](runners/resource-run.sh)、[Governance runner](runners/governance-run.sh)、[Network runner](runners/network-run.sh)、[supervisor](regression-supervisor.txt)。

| 仓库/门禁 | 实际命令 | 退出码 / 状态 |
|---|---|---|
| Resource生成 | `make generate TOOLS_DIR="$TOOLS"` | 0，manifest files=[] |
| Resource默认门禁 | `make verify TOOLS_DIR="$TOOLS"` | 0 |
| Image PG/并发/隔离/进程恢复/race | `./scripts/image-integration -v -race` | 0；44.578s，无race报告 |
| Image mutation | `SQLC="$TOOLS/sqlc" ./scripts/image-integration --mutations` | 0；3个破坏版本的业务断言分别exit1，源码恢复校验通过 |
| Resource跨仓测试进程 | `go test -mod=readonly -tags=imageintegration -race -c -o <run>/state/resource-contract-<SHA>.test ./internal/data/image` | 0，完整race插桩；二进制SHA256见下文 |
| Governance生成 | `scripts/generate-image-slice.sh`及变更Go文件gofmt | 0，manifest files=[] |
| Governance默认优化build | `go build -mod=readonly ./app/admin/service/cmd/server ./app/admin/service/cmd/admin` | 0 |
| Governance合同/权限 | `go mod tidy -diff`及runner的imagecontract、sql/bootstrap、auth、logging、constants、rpc包测试 | 0 |
| Governance既有回归 | `scripts/image-joint-integration --regressions` | 0；data/service/server及cmd默认优化 |
| Governance真实HTTP/JWT/AK/mTLS | `scripts/image-joint-integration`，绑定最终Resource二进制 | 0 |
| Governance有界race | Image定向`go test -race`及`scripts/image-joint-integration -race` | 0；真实HTTP用例7.25s，无race报告 |
| 旧Network全量PG | `make integration TOOLS_DIR="$TOOLS"` | 0；Network data647.120s |
| 旧Network全量race | `make race TOOLS_DIR="$TOOLS"` | 0；Network data833.486s，无race报告 |
| 旧Network租户mutation | `make tenant-mutations TOOLS_DIR="$TOOLS"` | 0；6个破坏版本被业务断言杀死，SQL/生成源码恢复校验通过 |

13个进程恢复场景均真的启动子进程：注入边界退出73，恢复进程退出0；不是只重新构造Go对象。跨仓测试用 Resource二进制 SHA256 为 `4e91c812179f2384d80071d7a4da22a8440eaa5925c2073dde51084a7bfaff78`，Resource编译保留默认优化与全部race插桩。

Governance race仅沿用已记录的受限编译例外：`GOFLAGS='-p=1 -trimpath -gcflags=go-wind-admin/app/admin/service/internal/data/ent=-l'`，`GOMAXPROCS=1 GOMEMLIMIT=768MiB GOGC=50`。只关闭生成Ent包内联，不关闭race插桩或删除断言。其默认优化build已单独exit0；本轮没有宣称默认优化race通过，上一轮OOM仍见 [原始记录](../img-20260929T1718Z/IMG-06-governance.md)。

串行 supervisor 于 2026-09-30T03:35:18Z 开始，04:17:24Z 结束，exit0，在5400秒总时限内完成。所有最终候选门禁 exit0；完整 [执行结果清单](test-results.json) 保留38组记录，包括5组有效预期red、1组独立夹具失败和32组pass。每组都有实际 `output.txt`、`exit`，没有把预期失败改写为退出0。

### 清理、回传与最终源码关联

[清理复查](cleanup.json) 对26个本轮实际 Docker ID逐一执行 `docker inspect`，全部 exit1 且明确 No such object；各 fixture trap 已删除自己的 PG/Redis、数据库/角色及私有 DSN 目录，跨仓 Resource helper 正常退出并清理。没有共享资源创建或共享资源清理。本轮 Network mutation 恢复了原 SQL 和生成查询；两仓 Fedora 测试源码 checkout 干净。

归档包 `public-return-v1.tar` 的 SHA256 为 `b818792df202ad4b407f62d42ed7cbdcd1fea629ed5df3a678159fe20dc0c541`，Fedora 与本地转运后相同；106个文件按白名单迁入本目录，原README保留。归档 [SHA256SUMS](SHA256SUMS) 覆盖105个证据/运行器/清单文件，不包含本README和SHA256SUMS自身。未携带凭证、证书私钥、二进制或缓存；归档动作在 Fedora 预算范围内 exit0。

收尾 `git diff --cached --check` exit2 仅来自原样日志的空白：Governance red 的 testify 输出和 supervisor 命令行末尾空格。保留原始字节与哈希；排除这两份原始日志后同一 Git 检查 exit0，没有格式化或重写历史失败输出。

[Resource运行源码清单](resource-runtime-source.json) 记录测试 SHA 的所有 Git blob/mode，仅排除唯一状态账本和本次证据目录；[Governance运行源码清单](governance-runtime-source.json) 不排除任何路径。最终收尾提交仅允许修改前述 Resource 两个文档位置；[Fedora核对程序](runners/doc-check.py) 会对最终精确 SHA 逐项比较清单、检查工作区及本轮 Markdown 目标/锚点。运行、测试、规格、生成及构建输入全部必须与本表已测 SHA 一致，不通过泛化“仅文档”判断继承测试结果。

## Live 与交付边界

本轮复核完整产品 live profile 仍为 approved=false、无 Governance端点/测试两仓SHA；另一份 approved=true 的 profile明确仅限技术夹具、非IAM租户/产品API。没有执行任何共享API读写、产品部署或共享配置修改。

原 `scripts/image_smoke.py` 仍使用默认严格 CA 校验及匿名 systeminfo版本字段假设。当前 Fedora Python 默认 strict flag已核对开启；上一轮 CA缺KeyUsage和认证版systeminfo观察仅作为历史证据，未在本轮重新对共享环境探测。未关闭 TLS/主机名校验、替换共享 CA、伪造 profile 或以技术Pod驱动冒充原smoke通过。

后端 Harbor smoke为blocked；下一步需明确覆盖本次产品调用的授权profile、部署端点/精确SHA/租户凭证，并在授权范围内解决严格CA兼容性和认证版本探测。IMG-08、IMG-09继续按用户授权保持blocked；本轮不宣称Image MVP或产品验收完成。

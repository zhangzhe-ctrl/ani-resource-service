# 2026-11-04：Res 网络状态消费简化实施记录

## 输入和实际执行范围

用户授权按已审的方法/字段级方案，直接在最新 Res 代码执行简化：完整采信 KCN 状态，Res 负责自己的管理工作。此次没有 KCN 改造前置条件。

- 仓库：`zhangzhe-ctrl/ani-resource-service`。
- 基线：main `5dc539d92e3150a3593bde3668082753aa4fdebe`，`fix(network): 隔离观测异常，恢复 VM/VNIC 正常准入并收敛终态扫描 (#54)`。
- 工作分支：`work/network-status-authority`。
- 设计输入：[已审实施方案](../../plans/network-management-simplification.md)。当前合同：[网络状态消费规格 v3](../../specs/cr-observation.md)。
- 实际执行位置：本地工作区，承担源码编辑、Git 和轻量静态检查。重任务仍限定在 Fedora；真实集群范围仍为 10–12。

本记录中的源码改动尚未形成可编译发布的完成态：sqlc、protobuf 和 typed config 生成物仍为基线版本，必须完成仓库固定工具链的生成，再编译和运行测试。没有以手工编辑生成物替代生成流程。

## 已落到源码的改动

| 范围 | 方法和字段落点 | 当前行为 |
| --- | --- | --- |
| 共同状态 | `ProviderObservation.Ready/StatusReason`、`Progress.ObservationError`、`Worker.Step/ensure/delete` | 直接映射原对象状态；读错保留成功投影；历史已完成 operation 不再更新。 |
| 事件与排程 | `KCProvider.EnableObservation`、`KCObservation.changed`、`Postgres.NotifyObservedObject/WakeManagedTasks`、六类任务 Finish | informer 只通知精确对象键；直接 GET 当前声明；通知计数、失败退避和原有事务租约负责本服务执行。旧证据索引、readset、审计、tombstone 和证明覆盖逻辑删除。 |
| 租户准入 | `AcceptSubnet/AcceptVNic/ReserveVNic`、Attachment Prepare、Egress/LB 受理和 VM/Instance 网络选择 | 依据 Res 租户、placement、用户意图、引用和版本；移除 Provider Ready、时效、父对象已回 UID、地址已经生成等条件。 |
| Attachment | `AttachmentWorker.Step`、`AttachmentAddressObservation`、`current_vnic_name/current_vnic_uid/current_mac_address/current_ips` | 管理生命周期直接消费当前计算侧快照；地址为独立展示输出。删除历史控制器链、VNicIP/Nic 关系与清场证明。 |
| 计算侧通知 | Instance `NetworkRuntime.NotifiedSnapshotRevision`、VM `NetworkPlan.NotifiedSnapshotRevision`、`UseNetworkNotifier` | 现有计算快照发生变化后通知 Network；跨库通知确认存在原 JSON，失败复用既有工作重试，不引入网络状态判断或新任务表。 |
| Egress / Pool / Switch | `observeEgress`、defaults 查询、管理员 verification、VirtualSwitch 原节点声明 | 消费原生 Ready/phase；管理员材料只存档；移除节点/镜像/物理拓扑的运行验收。 |
| LB | `ObserveLoadBalancer`、组件状态映射、`finishLB`、明确的 capability reader | 只处理自己声明的组件及原生配置确认；删除生成资源库存和 VIP/数据面回收证明。公开旧数据面字段返回 UNKNOWN。 |
| 安全组 | `SecurityGroupWorker`、`observeTargetSecurityGroups`、当前 consumer VNic 查询 | 现有 worker 处理五类目标；applied IDs 只来自原生状态，PATCH 成功只记 configured；结束目标同事务退休任务。 |
| SQL / schema | 查询源、`sqlc.yaml`、迁移 `0032_network_management_simplification.sql` | 移除证明列、关系 JSON、生成资源表；增加六类任务的读错字段及缺失的通知字段；搬运旧 Attachment 当前地址输出。 |
| 装配与合同 | cmd、service、observability、proto/config、RBAC | 取消旧证据依赖注入和权限；保留旧 API 字段号及废弃配置解析，删除运行门槛和证明指标。 |

0032 采用协调升级，旧写入者需要先停止；迁移保留状态、用户意图、原 UID、幂等回执和 operation 历史，重新安排现存非终态任务。该迁移删除旧代码依赖的列和表，不能把旧二进制回切作为回滚方案。本次未执行迁移。

## 验证事实

`pass` 只表示表中列出的轻量检查通过，不代表 Go 编译或数据库执行通过。

| 项目 | 结果 | 证据边界 |
| --- | --- | --- |
| 固定源码身份 | pass | 原提交及 tree 身份已核对；实施所需源文件从该版本取得，逐 blob 校验。没有用重建的合成提交替代 main。 |
| YAML 解析 | pass | `sqlc.yaml`、`configs/config.yaml` 及三个变更 RBAC 文件使用现有 Python YAML 解析器检查。 |
| 文档与退休路径 | pass | 新规格、方案、记录的本地链接目标及执行状态锚点存在；已明确退休的生产文件实际删除，不只是停止引用。 |
| Git 差异格式 | pass | `git diff --check` 无输出；此检查不能替代 gofmt、编译或测试。 |
| 旧接口和字段静态检索 | pass | 按范围检查已移除网络 proof/audit/TTL/关系字段的生产调用；历史迁移、兼容 protobuf 和其它领域的自有生命周期不作为残留网络证明。 |
| SQL 参数与调用方人工/文本核对 | pass | 已检查查询命名参数、返回行嵌入结构、exec/execrows 调用形式以及读错保留分支；不是 sqlc 或 PostgreSQL 的解析结果。 |
| 新旧测试调整 | not_verified | 已修改或新增状态三态、读错保留、UID/404、管理生命周期、通知并发、SG applied、LB 声明及 0031→0032 迁移回归；没有执行测试，删除旧证明合同的用例不算测试通过。 |
| `make generate` / gofmt | not_verified | Fedora 连接不可用；生成物尚未刷新。 |
| `make verify` | not_verified | 依赖正式生成及 Fedora；未执行 Go test/vet/build 或仓库完整门禁。 |
| PostgreSQL 集成与升级 | not_verified | 未创建数据库、运行迁移或执行 PG 用例。 |
| 真实集群与业务验证 | not_verified | 本次未对 10–12 或其它集群执行操作。 |
| 提交、推送、PR、部署 | not_verified | 没有执行。`AGENTS.md` 要求提交前运行 `make verify`；本次不将未生成、未验证源码提交为已完成变更。 |

## 实际阻塞

当前执行环境中，`ssh -o BatchMode=yes -o ConnectTimeout=8 fedora true` 返回 `Could not resolve hostname fedora: Temporary failure in name resolution`。历史指定的 `/home/chabking/.ssh/config` 及当前环境 `/root/.ssh/config` 均不存在，`SSH_AUTH_SOCK` 未配置。本地没有使用替代工具链开展生成、编译、测试或 PostgreSQL 工作。

需恢复当前任务对指定 Fedora 的 SSH 访问，才可继续隔离源码传输、固定工具链生成及必要验证。用户已授权继续实施，不需要再次确认简化方案。

## 尚待完成的执行步骤

1. 将包含本轮未提交修改的精确源码快照传至 Fedora 的独立任务目录，核对文件清单与 SHA-256，沿用既有重任务资源限制。
2. 在 Fedora 运行仓库固定版本的 `make generate`，取回并检查生成差异；解决正式生成/编译发现的问题。
3. 运行 `make verify`，以及本次状态消费、管理事务、通知恢复与迁移所需的 Network/Instance/VM PostgreSQL 回归；如发现失败，保存原始输出并修复后复验。
4. 将实际通过、失败和未验证分别补入本记录；通过提交门禁后再形成正式提交。部署及真实环境验收另记实际执行事实。

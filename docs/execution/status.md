# Network 执行状态

更新日期：2026-09-22。本文件是唯一当前进度入口；[规格](../specs/vpc-subnet.md)描述目标，[计划](../plans/vpc-subnet.md)描述工作包。

## 2026-09-22：Resource 改名与 Network 模块整理

用户最新明确要求直接交付 main 并将原仓库改名，取代此前等待 Public 补验后发布的顺序。已将包含 Governance 增量的 `9fad5b2` 快进推送到 main，原仓库改名为 `zhangzhe-ctrl/ani-resource-service`，repository ID `1362505185` 保持；正式本地目录为 `/home/chabking/workspace/ani-resource-service`。这项发布决定不把既有 fail / not_verified 改写为 pass。发布、模块消费、CI 和本地原始改动保护见 [发布收尾记录](records/RESOURCE-MOD-20260922/release-20260922/README.md)。

用户后续授权将本地 `3e40bb0` 合入改名实施分支并推送；[整合记录](records/RESOURCE-MOD-20260922/governance-merge-20260922/README.md)区分该业务增量与此前改名验证。合并后运行源码已经增加只读入口与错误分类行为，下面的历史门禁/控制面结果不自动覆盖这次合并快照，受影响检查已在 Fedora 通过 verify、真实 PG integration（20m 上限）和定向 race，命令与退出码见整合记录；完整新版本 R4 仍未验证。

状态 `in_progress`（9e491fa 精确提交的 Fedora verify/audit 与两条 CI 通过；最新真实控制面同库接管、恢复、回退、API 清理通过；Public 及必需数据面验收仍不完整；已按最新授权交付 main 并改名），完整 R0—R5 尚未完成。独立分支 `codex/resource-service-modularization`，
工作树 `/home/chabking/workspace/.worktrees/resource-service-modularization`；原工作树和历史保留。
[实施记录](records/RESOURCE-MOD-20260922/README.md)及[改名清单](records/RESOURCE-MOD-20260922/rename-inventory.md)。

| 阶段 | 当前证据 | 结果 |
|---|---|---|
| R0 | HEAD/main、dirty 文档、Fedora/三节点身份和安装 fingerprint 冻结；旧版 verify/build | pass；共享 Public 后续替换记录见下文，R4 写入前须重核环境 |
| R1/R2 | 新 module/cmd、三层 Network 整理、固定生成、descriptor 精确差异、递归违规 fixture、候选 verify/build | pass；故障注入入口已编译验证 |
| 兼容增量 | 独立旧客户端、旧库、cursor/回执、候选在途任务原版恢复、反向旧消费者、API 清理 | pass（受控 PG/Provider） |
| R3 | 安全修复 8153af4 的 tools/verify/integration/race/tenant-mutations pass；后续 7b39e53 完整 audit、verify、构建、镜像入口检查 pass；运行源码逐项相同 | pass；原失败及精确公开元数据例外的 24 个正反例保留 |
| R4 | 9e491fa 运行源码的真实同库链控制面 pass：21 个原对象接管、候选正常变更/强杀恢复、23 个对象回退、候选再次启动及 API 清理；[本轮记录](records/RESOURCE-MOD-20260922/control-plane-20260922/README.md) | 整体 not_verified：Public 产品链缺合法平台前置；本轮未跑数据面矩阵，既有严格六次计数 fail 与 Public 两轮 36/36 超时保留 |
| R5 | 已按最新用户指令交付 main，原 GitHub 仓库及本地正式目录改名，remote/worktree 引用修复；精确最终 SHA、CI、干净模块消费及供应链回执见发布收尾记录 | 发布动作 pass；其余逐项按回执判定，不代表完整 R4 通过 |

后续安全依赖修复已通过门禁：原候选 grpc v1.82.1 的 GO-2026-6443 / GO-2026-6348 使 audit fail；
用户已同意升级到同时消除漏洞的最低稳定版本。gRPC v1.83.2 新扫描零命中，完整 Fedora 门禁已通过；
具体依赖与逐项结果见[安全修复记录](records/RESOURCE-MOD-20260922/security-followup/README.md)。
用户先授权保留并复用原 Public，随后明确改为删除旧子网、创建公用子网并重启 kcn-controller；最新指示已执行。
[共享 Public 操作记录](records/RESOURCE-MOD-20260922/shared-public-20260922/README.md)：旧池正常删除，新 `kcn-system/public` 为 All/Ready；三个 EIP 和两个探针 Pod 同名同地址迁移，UID 变化已记录。控制器重启后 3/3 可用，镜像与其他配置保持。
但重启后两轮有界通信各 36/36 超时，原因未明；不算 R4 通过，不自动归因于改名或历史偶发。本轮实际调用证明：隔离库中原设备不存在、Public pool 列表为空，原版/候选/回退原版 CreateEIP 均返回 PUBLIC_EGRESS_NOT_READY。这里缺少的是 Network 数据库中的合法设备/池产品记录；非 Public 同库控制面链路已完成，Public SNAT 和 Public/双入口 LB 产品链仍未完成。
此前[手工池复用诊断](records/RESOURCE-MOD-20260922/public-reuse-20260922/README.md)保留原时点结论；原 R4 采样重叠 fail 同样保留。
修正后的不重叠驱动已留存但本 run 未重测，全部原始请求与失败保留。

用户随后要求继续测试并如实记录。新增 [2026-09-22 控制面续测](records/RESOURCE-MOD-20260922/control-plane-20260922/README.md)使用同库、同配置、原旧客户端完成候选变更、在途任务恢复和原版回退，migration checksum/applied_at、回执/cursor 与对象身份保持。新建 4 个 Subnet（含两个增量）、1 VPC、1 private LB、基础 Intranet EIP/SNAT 和两个 Attachment 均按 API 清理；占用为 0，19 个支持对象、进程、PG 与临时隧道已清理，17 个共享保护对象保持。初次等待超时、过早断言及运行器错误均保留，不将最终 pass 覆盖这些失败。该续测没有完成原 R4 数据面验收；后续用户明确要求直接交付 main 并改名，按本节最新发布记录执行。

故障重启后曾短暂触发原有租约/新鲜度保护，HTTPRoute 后端移除；自然恢复前的比较 fail 与
恢复后 pass 分开留证，不声称故障期间无中断。清理与发布证据见实施记录。
只有所有必需验收通过才完成 Goal。下述历史证据保持其原时点与范围。

## 既有 Network 实施状态：NET-VPC-LB-02

2026-09-18 [LB 正式修复及 CI 修正](records/NET-VPC-LB-02/lb-fix-20260918/README.md)已实现：LB 关系核验前后两处审计失效均改为在原调用截止时间内重新完整采集，保留身份、时效与并发检查；无截止时间调用仍最多一次刷新，取消/超时不返回可用证明。原代码在私网/双入口均能复现剩余约 27 秒却提前 unknown/PROVIDER_UNAVAILABLE；修复后的真实 PG/adapter/worker 单次调用在约 5.5 秒内 configured/fresh，Provider 对象摘要不变。Fedora UTC 环境中相关 PostgreSQL/race 27 项顶层测试、54 项子测试及 `make verify` 均 pass，包含六种真实进程恢复场景。四个隔离 PG 容器已清理，本轮没有集群写入。首次修复提交 `a7ff144` 已推送原分支，其 CI 的 Verify 与完整 PG/进程恢复均 pass；全仓 race 触发 Go 默认整包 10 分钟时限，未报告竞争或断言失败。现为全仓 integration/race 显式设置 20 分钟测试预算；产品请求超时不变。预算补正提交及后续 CI 以最终发布回执为准。此结果不扩展为真实双入口稳定窗口或所有历史退化均修复。

上一提交 `83a4346` 的两组 CI 失败也已定位：进程测试缺少必填健康检查端口，现已补齐；更新回执比较是 UTC 环境下 `time.Local` 与 `time.UTC` 的相同时刻被 reflect.DeepEqual 误判，现改为比较完整 JSON 回执，产品幂等实现未变。原始 red 与隔离字段差异留证。前序 [LB 分支诊断](records/NET-VPC-LB-02/lb-branch-20260917/README.md)保留其诊断时点事实。

2026-09-17 [失败分支诊断与修正](records/NET-VPC-LB-02/base-branch-20260917/README.md)完成验证：正常 Watch 连续续接令关键审计再次失效，Egress 在仍余 21.5–25.3 秒时提前返回 ProviderTemporary，导致 SNAT 应用事实未知、基础连接退化。修正改为原调用截止时间内重新采集，保留身份/时效/并发检查；不是 worker 未调度。真实同对象窗口由修正前 4 段退化，变为修正后 600 秒内数据库/API **602/602 ready、0 stale、0 应用事实未知**，覆盖 24 次来源续接。受控回归原代码 red/修正 green，相关 PostgreSQL/race 44 项顶层测试及 `make verify` 通过；首轮测试运行器超时及后续剩余选集结果分别留证。临时定点日志已移除。隔离产品、支持资源、运行进程及 PG 容器均已清理，私有备份保留；已有 Public Subnet UID/generation/spec 保持。交付目标为原分支，提交与远端一致性以最终回执为准，未合并 main、未部署，不将本次修正认定为整个 Goal 完成。

前序[独立 CR Watch 对照](records/NET-VPC-LB-02/base-watch-20260917/README.md)曾捕获 5 段 degraded：五条 Watch 和 CR 身份/版本保持，临时观察失败使 SNAT 应用事实未知，随后重试恢复。该阶段尚未确认具体失败分支；上段定点诊断已补齐原因。两轮观察均不等于业务流量中断证据。

2026-09-17 本次用户要求的健康检查端口真实闭环已通过：[补验与发布记录](records/NET-VPC-LB-02/health-port-live-20260917/README.md)。Fedora 上完整 `make verify` 通过；独立 PostgreSQL 正常迁移到 0008；Network API 传入 listener=8081、backend/health=8080，自动生成两个 Backend 和显式健康检查配置；ani-02/ani-03 经 VIP 各 6/6 HTTP 200，覆盖两后端。只有这一轮有限流量，不宣称持续健康或观察稳定性已修复。本次测试产品资源、支持 namespace/RBAC 与隔离运行进程均已清理，私有 PG 备份保留。

本次代码交付分支为 `codex/net-vpc-lb-02`，固定基线 `e534bb0e8ef83055e18e91d1d41a6c821348a887`，按用户授权本地提交并推送，远端 main 保持。提交和远端一致性以该分支 Git 历史及最终回执为准。已有 Public Subnet `pubdebug-20260917-public` 保持；Public/双入口/SNAT 功能按用户决定正常处理，残留偶发超时交接 kcn。

本次已解决基础连接 Egress 采集的提前失败，完整 Goal 仍保留独立的 LB 观察稳定性未解决项；管理员二层诊断接口仍为提案，真实 IAM/UI、生产/正式存量迁移、容量/HA 保持 not_verified。本次补验和代码交付完成不自动改写这些范围。

### 2026-09-17 本次补验之前（历史）

2026-09-17 显式健康检查端口已实现：[增量与验证](records/NET-VPC-LB-02/health-port-20260917/README.md)。创建/更新必填且匹配全部后端端口，存储/查询/Provider 映射贯通，追加迁移 0008；Fedora 固定生成、局部检查及服务编译通过。按用户要求未跑完整门禁/PG/集群，未部署，Public Subnet 保持。历史完整门禁不自动覆盖新源码。

2026-09-17 用户决定：当前手工 Public/双入口/SNAT 功能按正常处理，残留偶发超时留给 kcn 团队，停止追加循环复测。[修复总结与处置](records/NET-VPC-LB-02/public-review-20260917/README.md#用户结论功能按正常处理残留超时交接-kcn)保留全部原始失败；不将功能处置等同于无超时或产品 API 完整重验。

2026-09-17 10:48 UTC 双入口 EIP [再次复测](records/NET-VPC-LB-02/public-review-20260917/dual-recovery-retest.txt)：public-a 首次超时、随后 5/5 为 200；public-b 6/6 为 200。已有恢复流量但本轮仍有失败，稳定恢复未确认；未改配置或删除 Public Subnet。

2026-09-17 其他类型手工复测：双入口私网 8/8、public-b EIP 8/8 成功，public-a EIP 8/8 超时；Public SNAT 三来源 HTTP、真实源地址 .196、停用/恢复通过；原纯 Public LB 两来源回归通过。Public Subnet UID 保持，public-lb/dual-lb/已启用 public-snat 现场全部保留。[记录与证据](records/NET-VPC-LB-02/public-review-20260917/README.md)不替代历史产品 API 验收。

2026-09-17 10:37 UTC 当前手工纯 Public LB [复测](records/NET-VPC-LB-02/public-review-20260917/public-b-recovery-retest.txt)：public-a/public-b 各 6/6 HTTP 200，分别覆盖两个后端，之前 public-b 超时未复现。本轮未改集群，Public Subnet UID 保持。该有限手工检查 pass；双入口、Public SNAT 和产品 API 验收结果未更新。

2026-09-17 后续授权已补齐手工现场两个 Backend：Route ResolvedRefs=True，配置收敛后 public-a 经 EIP 访问连续确认 4/4 HTTP 200、覆盖两后端；public-b 仍超时。Public Subnet 保留，双入口/SNAT 未重测，详见[补齐与复测](records/NET-VPC-LB-02/public-review-20260917/README.md#后续授权补齐并复测)。下段为补齐前只读时点。

2026-09-17 [同事排查结论只读复核](records/NET-VPC-LB-02/public-review-20260917/README.md)：保留现有 Public Subnet；当前手工现场确实缺 Backend，Route 报 BackendNotFound；健康检查省略 override 时默认 endpoint 端口，未证明该省略是故障原因。手册已撤去平台删除/重建步骤。本轮未改集群、未重测数据面，不改变下述历史验收结果。

状态：`blocked`，完整 Goal 未完成。本轮已经收尾，停止追加测试。固定基线 `e534bb0e8ef83055e18e91d1d41a6c821348a887`，工作树 `/home/chabking/workspace/.worktrees/network-vpc-lb-02`，分支 `codex/net-vpc-lb-02`；未提交、推送或发布。

15:06 UTC [阻塞审计](records/NET-VPC-LB-02/device-registration-20260915/goal-blocked-audit-20260915T1506.json)核对连续三轮相同的 Public 必需验收失败与用户暂缓决定。上一轮完成修复、验证和清理，属于实质进展；本轮只读确认 r28 inactive/MainPID=0、备份完整、260 项合格源码未漂移。无运行中的验证任务可等待，不自动重建双入口现场或重跑门禁。双入口观察根因仍未确认，与三项 Public 流量失败分别保留；恢复依赖重新开展已暂缓的 Public/双入口现场排查。

[最终交接](records/NET-VPC-LB-02/device-registration-20260915/final-handoff.md)与[11 项矩阵](records/NET-VPC-LB-02/device-registration-20260915/u09-matrix-20260915T1500.json)确认 **8 项 pass、3 项 Public fail**。三项分别为纯 Public EIP 入口、双入口 EIP 入口、Public SNAT 实际出站/源地址，按用户要求[留待后续排查](records/NET-VPC-LB-02/device-registration-20260915/public-forwarding-followup.md)；插件或物理网络根因未确认。另有双入口 LB 观察稳定性 fail，不能仅凭该状态判断断流；本轮不继续循环采样。

最新合格候选 `20260915T132645Z-482e616d` 完成[完整门禁](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-candidate-gates.json)：421 个唯一测试/子测试通过，260 项运行/测试/构建配置源码一致，范围外容量未运行。[Attachment 修复及 Watch 窗口](records/NET-VPC-LB-02/device-registration-20260915/attachment-audit-recovery.md)记录 41 次来源边界下 private 144/144 configured/fresh，私网有限流量验证通过；双入口仍有 22 次 ProviderNotReady、11 次 ProviderUnavailable。同名新 UID Service 的[真实保护、恢复与清理](records/NET-VPC-LB-02/device-registration-20260915/same-name-service-uid-assessment.json)通过。

[产品清理完成](records/NET-VPC-LB-02/device-registration-20260915/final-cleanup-assessment.json)：两租户 LB/VPC/Subnet/EIP/SNAT、六个业务 Attachment/Pod、三个 Public 探测 Pod，以及地址池/网关/VLAN 已终结；活动 EIP/VIP/子网占用为零，Provider 列表为空。[保留清单](records/NET-VPC-LB-02/device-registration-20260915/final-retained-inventory.json)包含 ens35 登记、采集器与支持 SA/RBAC/namespace。设备退役是单独基础设施操作，kcn-config data/managedDevices 与共享默认 VPC 保持，不能把保留部分称为已全部清空。

本 run r28 已于 14:58:59 UTC 停止，[最终备份](records/NET-VPC-LB-02/device-registration-20260915/final-cleanup-checkpoint-1500.json)在 ubuntu/本机 SHA 一致，PG 容器及私有恢复配置保留。后续只在明确的故障恢复工作中继续，不重跑已通过的门禁或已归档的 VLAN 恢复。管理员二层诊断接口仍为[提案](records/NET-VPC-LB-02/l2-diagnostic-20260915/interface-proposal.md)，没有实施。真实 IAM/ANI 界面、互联网、生产/正式存量迁移、容量/HA 保持 not_verified。

| 卡 | 实现与验证 | 剩余 |
|---|---|---|
| NET-U05 | implemented，领域/RPC/PG/租户与迁移门禁通过 | 真实集群结果归 U09 |
| NET-U06 | implemented，三类创建、更新、删除/重绑及身份保护通过 | Public 数据面失败归 U09 |
| NET-U07 | implemented，并发/恢复、权重/健康及 Attachment 修复验证通过 | 双入口观察稳定性 fail |
| NET-U09 | 8 pass / 3 fail，产品生命周期清理完成 | 三项 Public 待排查，基础设施登记保留 |

以下内容保留各历史时点的状态和证据，其中“待完成”“正在恢复”等描述不代表本段最新状态。

### 2026-09-15 15:00 UTC 之前的执行过程（历史）

状态：`in_progress`，完整 Goal 未完成。用户要求的 kc 预受管设备登记修正已实现，并经三节点真实采集和 `AdoptNetworkDevice` 验证：`managedDevices` 与整个配置 data 保持原样，只登记受保护的 Network binding。方案、计划与操作说明已同步，详见[修正及恢复记录](records/NET-VPC-LB-02/device-registration-20260915/README.md)。

固定基线 `e534bb0e8ef83055e18e91d1d41a6c821348a887`，工作树 `/home/chabking/workspace/.worktrees/network-vpc-lb-02`，分支 `codex/net-vpc-lb-02`，成果未提交、推送或发布。[授权目标](records/NET-VPC-LB-02/goal-objective.md)、[输入](records/NET-VPC-LB-02/inputs.json)、[运行索引](records/NET-VPC-LB-02/run-index.json)和[源码覆盖](records/NET-VPC-LB-02/source-coverage.json)区分固定基线、未提交源码与实际验证候选。

14:40 UTC 清理进度：[同名新 UID Service 负例与恢复](records/NET-VPC-LB-02/device-registration-20260915/same-name-service-uid-assessment.json)已通过，原双入口 LB 删除成功；后续[private LB、两 Public EIP、legacy Attachment 清理](records/NET-VPC-LB-02/device-registration-20260915/final-cleanup-progress-01.json)已完成，六个 Attachment 与所有 LB 子网占用均已[释放](records/NET-VPC-LB-02/device-registration-20260915/final-cleanup-progress-02.json)。三个 Public 探测 Pod/VNic/VNicIP 的[清理核验](records/NET-VPC-LB-02/device-registration-20260915/final-public-probes-verify.json)通过；VPC、Subnet、基础 EIP/SNAT、池/网关/VLAN 清理仍在推进。r27 被[共享 load 9.40 的资源保护](records/NET-VPC-LB-02/device-registration-20260915/runtime-r27-resource-stop.json)停止，PG 保留；正在按原限制恢复同一合格构建，失败的后续 RPC 未受理。设备注销不属于现有产品 Delete，ens35 登记与恢复数据库保留，未直接修改 kcn-config。

最新合格候选为 `20260915T132645Z-482e616d`：[Attachment 重试修复](records/NET-VPC-LB-02/device-registration-20260915/attachment-audit-recovery.md)已通过完整 421 项测试/子测试及构建。[r26](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r26.json) 于 13:42 UTC 保留原数据库和配置启动；[私网 LB 实际读取](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-r26-initial-ready-corrected.json)与[双入口读取](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-r26-dual-start.json)已 configured/fresh。[固定 12 分钟 Watch 续接窗口](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-r26-watch-assessment.json)完成：41 次来源边界变化，private LB 144/144 configured/fresh、窗口内 12/12 私网请求通过，Attachment 历史无新增失败；双入口 LB 有 22 次 ProviderNotReady、11 次 ProviderUnavailable（其中 7 次过期），整体稳定性仍 fail。Public 三项失败按用户要求暂缓，未恢复转发探测。后续[同名新 UID Service 负例方案](records/NET-VPC-LB-02/device-registration-20260915/same-name-service-uid-plan.json)现已按原 Gateway 正常删除后注入同名新 UID Service、保留占用、按 UID 移除外来 fixture 并自动完成产品删除。

审计恢复候选 `20260915T121305Z-1e089142` 已完成[完整门禁](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-candidate-gates.json)：make verify、固定基线生成/合同、真实 PG race 及构建通过，420 个唯一测试/子测试通过，data 顶层 120/121，另 1 项为范围外容量未运行。相对上一合格候选只改两个 adapter 文件并新增一个回归测试文件，259 项运行/测试/构建配置源码已核对。缓存失效后在同次观察内重新完整采集一次，原调用预算及身份检查保持；[两个失败重现及四项针对性 PG race 回归](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-recovery.md)保留红/绿对照。r23 于 11:52 UTC [暂停并备份](records/NET-VPC-LB-02/device-registration-20260915/before-audit-refresh-red-1150.json)后，新构建[哈希核对和安装](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-candidate-deployed.json)完成；[r24](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r24.json) 于 12:27 UTC 保持原配置、数据库及资源启动，并通过[实际 RPC](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-r24-initial-ready.json)读取。[固定 30 次采样](records/NET-VPC-LB-02/device-registration-20260915/private-lb-r24-stability-assessment.json)全部 configured 且未过期，[同期 private VIP 12 次](records/NET-VPC-LB-02/device-registration-20260915/private-vip-audit-refresh-r24-assessment.json)命中两后端；但窗口结束后的实际读取再次出现 BackendIdentityMismatch，连续稳定性仍 fail。[Attachment 历史](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-r24-attachment-history.json)有短暂 ProviderUnavailable/恢复，[原后端身份](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-r24-identity-assessment.json)均存在且未被替换；不能把提示直接归为 UID 改变，其后 Attachment 临时诊断与隔离复现见下段。用户暂缓的 Public 问题保留原始 fail，未继续转发探测。

12:42 UTC 已[暂停 r24 并备份](records/NET-VPC-LB-02/device-registration-20260915/before-attachment-debug-1242.json)，转入[仅两个后端 Attachment 的临时诊断](records/NET-VPC-LB-02/device-registration-20260915/attachment-debug-plan.json)。诊断源码 `20260915T124319Z-f715d364` 仅完成编译，在 [r25](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r25.json) 运行；不继承完整门禁资格，配置、数据库、资源身份与正常产品行为不变。诊断已[捕获两次重试耗尽](records/NET-VPC-LB-02/device-registration-20260915/attachment-debug-assessment.json)：连续来源换代使审计失效，窗口 21 configured、6 BackendIdentityMismatch、3 ProviderNotReady。r25 于 12:53 UTC [停止并备份](records/NET-VPC-LB-02/device-registration-20260915/after-attachment-debug-1253.json)，[临时源码、二进制和私有注册已撤回](records/NET-VPC-LB-02/device-registration-20260915/attachment-debug-restored.json)；原合格构建保留，live 当前暂停。[Attachment 最小复现与修复](records/NET-VPC-LB-02/device-registration-20260915/attachment-audit-recovery.md)已在原 ubuntu 完成：隔离原实现连续两次提前失败，新实现两轮 race 及原截止时间、UID 拒绝、释放边界回归通过。新候选 `20260915T132645Z-482e616d` 已通过[完整门禁](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-candidate-gates.json)：421 项唯一测试/子测试、data 顶层 121/122，通过之外仅范围外容量未运行；260 项源码已核对。[合格构建已安装](records/NET-VPC-LB-02/device-registration-20260915/attachment-retry-candidate-deployed.json)，已在 r26 恢复原现场并开始真实 Watch 续接窗口验证，不能宣称真实稳定性已恢复。磁盘保护中止与[旧构建物本机校验转存](records/NET-VPC-LB-02/device-registration-20260915/historical-artifact-local-retention.json)独立记账，Public 探测继续暂缓。

当前候选还修正关联设备共用审计快照、事实不变的采集续报不使审计永久失效、读取 Attachment 后再用数据库时间判断 LB 后端新鲜度，以及对象集合摘要不受 Informer 返回顺序影响。失败重现与针对性回归保留在恢复记录。上一已部署源码候选 `20260915T051609Z-70cbcaa3` 的[完整门禁](records/NET-VPC-LB-02/device-registration-20260915/canonical-candidate-gates.json)一次通过 make verify、固定 e534 合同及真实 PG race：413 个唯一测试/子测试，117 个 data 顶层测试中 116 通过，范围外容量 1 个未运行。早期共享 load 保护中止与其他失败保留，不转记为本次成功。

07:18 UTC 起暂停本任务 r14、保留 live PG 并[备份](records/NET-VPC-LB-02/device-registration-20260915/before-cancel-recovery-20260915T071844.json)，转入用户要求的未知创建恢复排查。新增真实 PG [失败重现](records/NET-VPC-LB-02/device-registration-20260915/shutdown-outcome-red.json)证明正常退出取消导致确定的发送前失败无法落库；独立有界完成 context 修正已通过[lease/未知结果保护](records/NET-VPC-LB-02/device-registration-20260915/shutdown-outcome-green.json)及[真实 KC adapter 请求计数对照](records/NET-VPC-LB-02/device-registration-20260915/shutdown-adapter-green.json)。新增源码候选 `20260915T074304Z-31ea8959` 的[完整门禁](records/NET-VPC-LB-02/device-registration-20260915/shutdown-candidate-gates.json)已一次完成 make verify、固定生成/合同和真实 PG race，418 个唯一测试/子测试通过；data 顶层 118/119，通过之外的 1 个为范围外容量未运行。首次完整命令的临时路径过长失败及无源码变化的对照保留在[运行器诊断](records/NET-VPC-LB-02/device-registration-20260915/shutdown-gate-tempdir-diagnosis.json)。同一门禁构建已在 [r15](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r15.json) 恢复运行，配置和原数据库保持；新版本实际 VLAN 与 private VIP 复验见下方，其他历史 live 结果保留各自源码归属。

[外来同名 Service 保护及清理](records/NET-VPC-LB-02/device-registration-20260915/foreign-service-assessment.json)通过：创建/删除均阻塞于归属冲突，Service UID/spec/owner 保留；fixture owner 删除它后，产品 Delete 完成并释放 VIP/父占用，新增 RBAC 已删除。原 private VIP 的[复查 12 次流量](records/NET-VPC-LB-02/device-registration-20260915/private-vip-after-foreign-fault-assessment.json)仍命中两后端。

| 卡 | 实现状态 | 已获得验证 | 尚缺验证 |
|---|---|---|---|
| NET-U05 | `implemented` | 领域/RPC、真实 PG 原子受理、幂等/互斥、统一占用、租户 SQL/API/operation 负例、空库与 0006 升级 | 完整实际集群矩阵另归 U09 |
| NET-U06 | `implemented` | 实际 adapter 受控生命周期、未知写入/部分创建/删除恢复；三类 LB 实际创建，private 更新，Public 删除和 EIP 重绑 | Public/双入口的数据面失败另归 U09，双入口清理待完成 |
| NET-U07 | `implemented` | 双进程故障、父删除/更新竞争、默认池与 U08 组合；真实权重 0、TCP 健康失败/恢复 | 观察状态全程稳定未通过 |
| NET-U09 | `in_progress` | 两节点内网、private/双入口 VIP、SNAT 四阶段隔离、Public 删除/EIP 重绑、U08 组合流量和清理 | Public/双入口 EIP 与出站源地址实测失败；完整占用负例和整体清理未完成 |

真实 run 为 `lb02-09141908-2b3122`，数据库 `net_vpc_lb_02_2b3122`。新 VPC 自动产生基础 EIP/SNAT，两个 worker 上的 [DNS、CoreDNS 服务及 Envoy 平台服务访问](records/NET-VPC-LB-02/device-registration-20260915/base-intranet-probes-01.json)通过。原两个 VPC、三个 Subnet、六个 Attachment/owner 协议接入的业务 Pod 保留。业务镜像按 ubuntu → 本机 → 三节点构建中转，[OCI 索引/manifest/config 与实际二进制](records/NET-VPC-LB-02/device-registration-20260915/probe-oci-identity-chain.json)来源链已核对。

private LB 从同 VPC 客户端直接请求 `10.233.0.200:8080`，使用原 small 两副本规格。r12 的权重 0、恢复权重、单后端关闭/双后端关闭与恢复实际流量通过；同一资源在最终候选 [r13](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r13.json) 上[双后端 12 次](records/NET-VPC-LB-02/device-registration-20260915/r13-stable-assessment.json)、[添加成员 24 次](records/NET-VPC-LB-02/device-registration-20260915/private-vip-three-members-assessment.json)、[移除成员后 24 次](records/NET-VPC-LB-02/device-registration-20260915/private-vip-member-removed-assessment.json)全部通过。每次响应校验 run、实例和 nonce；移除后仅命中原两个后端。产品没有持续健康来源，`data_plane_state=unknown` 保持，不将探测写成持续健康。

观察稳定性仍有失败：r17/每类 4 worker 的[固定 30 次采样](records/NET-VPC-LB-02/device-registration-20260915/private-lb-r17-stability-assessment.json)也未通过（12 次 ProviderNotReady、18 次 ProviderUnavailable，其中 9 次 stale）；同一时期 [private VIP 12 次实测](records/NET-VPC-LB-02/device-registration-20260915/private-vip-after-public-delete-assessment.json)仍命中双后端，不能混同配置观察与流量。历史 [固定 30 次采样](records/NET-VPC-LB-02/device-registration-20260915/private-lb-r13-stability-assessment.json)为 29 configured、1 ProviderNotReady；未再出现 r12 那轮的 18 次 unknown，但不能宣称全程稳定。移除成员过程中也保留短暂 BackendIdentityMismatch，随后由正常 worker 收敛完成。基础连接在后续操作中仍有退化/恢复，原因不能仅由这些采样归为同一个缺陷。

U08 审核计划完成实际暂停/恢复和补齐 operation；[补齐前](records/NET-VPC-LB-02/device-registration-20260915/legacy-traffic-before-backfill.json)与[补齐后](records/NET-VPC-LB-02/device-registration-20260915/legacy-traffic-after-backfill-01.json)跨节点响应通过，历史 create_vpc 完成时间保持。后续新增双节点旧形态 VPC 的[跨补齐阶段采样](records/NET-VPC-LB-02/device-registration-20260915/u08-continuous-assessment.json)已完成：306 次请求无失败，覆盖补齐前/中/后，历史 create operation 与 Pod UID/IP 不变；最大单方向采样间隔 3.83 秒，不推断未采样瞬间。新增测试实例已按 owner/Attachment 协议及产品 Delete 完成[完整清理](records/NET-VPC-LB-02/device-registration-20260915/continuous-cleanup-assessment.json)。新增无业务旧形态 VPC 的[实际删除竞争](records/NET-VPC-LB-02/device-registration-20260915/u08-delete-race-assessment.json)通过：删除先受理，补齐返回 RESOURCE_BUSY，未创建补齐 operation，VPC 与基础资源无残留；该次实际竞争只观察到删除先行顺序。

[原 private LB 删除及 VIP/父占用释放](records/NET-VPC-LB-02/device-registration-20260915/private-lb-delete-assessment.json)与[无业务 VPC 系统基础资源按序清理](records/NET-VPC-LB-02/device-registration-20260915/base-cleanup-assessment.json)通过。同一 VIP 的新 LB `lb_cc562f7d8be147afae8144555aa7243b` 已完成创建，[复用后 12 次实际流量及 VIP 重复占用拒绝](records/NET-VPC-LB-02/device-registration-20260915/private-lb-vip-reuse-assessment.json)通过。完整 [U09 逐项证据快照](records/NET-VPC-LB-02/device-registration-20260915/u09-matrix-20260915T1240.json)明确未完成的 Public、完整负例，不以部分通过替代全项。临时测试均只操作本 run，原业务后端继续保留。最新[r24 数据库检查点](records/NET-VPC-LB-02/device-registration-20260915/audit-refresh-r24-checkpoint-1239.json)已完成本机/ubuntu 哈希核对，包含 Public LB 删除、SNAT 四阶段/重绑后解绑、双入口创建成功和跨 namespace 故障恢复；数据库及原两 VPC/六业务 Pod 保留。

历史未知 VLAN 已按用户明确授权[恢复并完成复测](records/NET-VPC-LB-02/device-registration-20260915/vlan-authorized-recovery-assessment.json)：一次性实验记录修复保留备份和原 pending 审计，随后原 create operation 由 worker 成功完成。再经产品 Delete 释放原 VLAN 占用，以同一 ens35、vlan_id=0 新建 `vlan_9b12fcb230ef4bc08ba1d497ebe44009` 成功；该轮真实流程未复现永久结果未知，按用户要求归档，不再继续历史请求轮询。通用恢复接口未实施，自动未知写入保护规则保持；本次人工恢复不记作自动产品恢复通过。共享 kcn-config 的 UID/data/登记标记保持，新版本 [private VIP 12 次复查](records/NET-VPC-LB-02/device-registration-20260915/private-vip-after-vlan-retest-assessment.json)全部命中原两个后端。

Public 网关与[Public 池](records/NET-VPC-LB-02/device-registration-20260915/public-underlay-pool-ready.json)已完成创建，172.16.102.0/24、OVN .2、上游 .1、实验地址 .192–.207。[平台实际资格验证](records/NET-VPC-LB-02/device-registration-20260915/public-pool-qualification-assessment.json)通过两个节点 HTTP 往返、跨节点请求和接收端观察源地址 .192/.193；原始源地址工具缺失失败及修正后的实测分别保留。真实验证已经 API 记录，随后[开启分配并设为本 run 默认池](records/NET-VPC-LB-02/device-registration-20260915/public-pool-admission-enabled-03.json)成功；早先观察过期及依赖暂不可用拒绝不转记为通过。上述资格只覆盖实验 Public 子网直接连接，不替代租户 EIP/SNAT 或 LB 验收。

Public EIP .194/.195 已由产品 API 分配成功。纯 Public LB `lb_bd6f74b7445b4c5a815763d38a995f14` 在 [r17](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r17.json) 达到 [Available/configured](records/NET-VPC-LB-02/device-registration-20260915/public-lb-ready-r17.json)，两个 Envoy 副本及生成 Service/EIP 占用身份合法。但从 VPC 外直接请求 .194:8080 的 [24 次验收](records/NET-VPC-LB-02/device-registration-20260915/public-lb-external-no-snat-assessment.json)全部超时，该时段[没有 Public SNAT](records/NET-VPC-LB-02/device-registration-20260915/public-lb-no-public-snat-final.json)。[对照](records/NET-VPC-LB-02/device-registration-20260915/public-entry-controls.json)确认同一外部客户端直连 Public 实验 Pod 成功；[被动抓包](records/NET-VPC-LB-02/device-registration-20260915/public-entry-packet-capture.json)看到 EIP ARP 回应及 ens35 上的重复 SYN，没有 SYN-ACK，尚未定位具体 Provider 转发环节。不能将该失败归为 VLAN 未贯通，也不能用直接 Envoy Pod IP HTTP 成功替代 EIP 验收。

Public SNAT `snat_c2e679a19be64badbc78877d8323451d` 使用另一 EIP .195，[配置启用及观察](records/NET-VPC-LB-02/device-registration-20260915/public-snat-ready.json)已完成；[真实出站连接和接收端源地址](records/NET-VPC-LB-02/device-registration-20260915/public-snat-source-enabled.json)失败，未获得 HTTP 响应或 .195 的接收端连接证据。同期 [private VIP 12 次](records/NET-VPC-LB-02/device-registration-20260915/private-vip-public-snat-enabled-assessment.json)仍通过，命中两个后端。[Public SNAT 启用、停用、重新启用、解绑的隔离验收](records/NET-VPC-LB-02/device-registration-20260915/public-snat-lifecycle-assessment.json)已经通过：四阶段 48 次 VIP 请求、32 个内网检查通过，VPC/基础 EIP/SNAT UID 保持，Public EIP 原 UID 保留且回到 Available。额外的关闭/解绑过程采样也通过；有限采样不等于全程无间断。纯 Public LB 的[产品删除](records/NET-VPC-LB-02/device-registration-20260915/public-lb-delete-assessment.json)已在 10:12 UTC 完成：生成资源、claim、组件与父占用全部释放，原业务 Pod UID 不变。首次等待早于 360 秒优雅退出期限，后续正常完成，没有强制清理。原 .194 的[SNAT 重绑](records/NET-VPC-LB-02/device-registration-20260915/public-eip-reuse-assessment.json)已实际完成，EIP 原 UID 不变；.195/VIP .201 的[双入口 LB 验收](records/NET-VPC-LB-02/device-registration-20260915/public-private-assessment.json)已实际执行：同一创建 operation 于 10:40 UTC 成功，small 两副本/生成身份正确；12 对并行入口请求全部重叠，VIP 12/12 成功、两后端各 6 次，EIP 0/12，整体双入口记 fail。重绑测试的 Public SNAT 已经再次通过产品解绑。

09:02 UTC r15 因原 ubuntu load1=8.42 受保护退出，随后 r16 同构建恢复完成原 EIP operation。09:25 UTC 在保存 [PG 备份](records/NET-VPC-LB-02/device-registration-20260915/before-worker-concurrency-0924.json)后，仅将隔离实例每类 worker 并发从 2 调为已支持的 4，详见[配置对照](records/NET-VPC-LB-02/device-registration-20260915/worker-concurrency-tuning.json)；r17 的二进制、数据库、租约与观察时限均不变。此后 Public LB 收敛，但不能据此认定既有观察稳定性问题已解决。基础内网两个节点的 DNS 和 DNS/Envoy HTTP [复查通过](records/NET-VPC-LB-02/device-registration-20260915/base-intranet-r15-renewal.json)，[Intranet 资格](records/NET-VPC-LB-02/device-registration-20260915/intranet-verification-renewal-r15.json)有效至 14:57 UTC。新增三个平台探测 Pod、两个最小 RBAC 对象仍保留，后续清理必须覆盖。

10:25 UTC [r17 因共享主机 load1=10.05 触发资源保护](records/NET-VPC-LB-02/device-registration-20260915/runtime-r17-resource-stop.json)，PG/集群对象保留；之后一次重绑 SNAT 删除 RPC 在连接 socket 前失败，未受理。资源回落后经 30 秒复核，以同一构建/配置/数据库在 [r18](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r18.json) 恢复，继续原双入口 operation，不重做任何初始化或旧 VLAN 恢复。

[首次超时对照](records/NET-VPC-LB-02/device-registration-20260915/audit-window-config-comparison.json)未启动：r19 被[既有配置上限](records/NET-VPC-LB-02/device-registration-20260915/r19-startup-diagnostic.json)拒绝，其 [30 次未连接采样](records/NET-VPC-LB-02/device-registration-20260915/private-lb-r19-stability-assessment.json)不构成产品观察结果。随后按现有上限[修正对照参数](records/NET-VPC-LB-02/device-registration-20260915/audit-window-config-comparison-02.json)：audit_timeout 10→15 秒、request_timeout 20→30 秒、lease 80→120 秒；每类 4 worker、新鲜度 60 秒、资源配额和二进制不变。此前 [r18 指标](records/NET-VPC-LB-02/device-registration-20260915/r18-observation-metrics-1043.json)有 10 次审计失败、成功采集最长 9.13 秒，根因仍需验证。已保留[变更前备份](records/NET-VPC-LB-02/device-registration-20260915/before-audit-window-1049.json)。[r20](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r20.json) 已通过实际 RPC 读取确认启动；[固定 30 样本对照](records/NET-VPC-LB-02/device-registration-20260915/private-lb-r20-stability-assessment.json)仍 fail：11 configured、17 ProviderNotReady、2 BackendIdentityMismatch。该窗口没有审计失败或 stale，放宽时限未解决稳定性。随后已[暂停并备份](records/NET-VPC-LB-02/device-registration-20260915/before-observation-debug-1125.json)，仅在本 run 准备临时日志。诊断构建 `20260915T112356Z-8a8f27c9` 在 r22 实际运行，[30 次采样](records/NET-VPC-LB-02/device-registration-20260915/observation-debug-assessment.json)27 configured、3 unknown，仍 fail；已捕获 Snat 审计快照失效、应用事实暂未知、LB 依赖检查失败的路径，但没有证明所有历史退化原因相同。临时源码和二进制[已经撤回](records/NET-VPC-LB-02/device-registration-20260915/observation-debug-restored.json)，[r23](records/NET-VPC-LB-02/device-registration-20260915/runtime-resume-r23.json) 恢复原合格构建并通过[实际 RPC](records/NET-VPC-LB-02/device-registration-20260915/qualified-restored-r23-read.json)，没有把诊断当成修复。

用户授权的[新集群与恢复输入](records/NET-VPC-LB-02/resume-20260915.md)保持：172.16.101.10–12，来宾 untagged，上游逻辑 VLAN 102，172.16.102.0/24，网关 .1。[ani-01 网关往返与临时配置清理](records/NET-VPC-LB-02/l2-diagnostic-20260915/untagged-followup.md)已保留；测试 VM 172.16.102.30 已由用户关机，不再重测。管理员二层排障接口仅有[建议](records/NET-VPC-LB-02/l2-diagnostic-20260915/interface-proposal.md)，没有扩大本批实现范围。

[跨 namespace 外来 EIP Service 保护](records/NET-VPC-LB-02/device-registration-20260915/cross-namespace-service-assessment.json)已通过：Tenant B 的独立 Service 引用 Tenant A 双入口 LB 的 EIP，LB 报告归属冲突；外来 Service 和合法生成资源 UID/spec 均保留。owner 按 UID 删除外来对象后正常 worker 恢复，新增两项 RBAC 已删除。此项不代替已有生成 Service 的 UID 替换场景。

按用户最新要求，三项 Public 失败停止继续排查并交接：[Public 转发排查记录](records/NET-VPC-LB-02/device-registration-20260915/public-forwarding-followup.md)集中保存地址、版本、成功对照、抓包、OVN 只读结果及复查命令。网络插件或外部网络配置只是待排查方向，根因未确认；原 fail 保留，整体 Goal 仍未完成。其余范围内验收、观察问题和清理继续独立记账。

整体产品生命周期清理未完成，不能先删除数据库/namespace 或强删 finalizer。原 kind 的 lb-strict-0911、新集群 lb-validation、既有工作树及共享资源保留。三类调用与恢复方法见[产品 API 手册](../../deployments/load-balancer/README.md)和[后续交接](records/NET-VPC-LB-02/handoff.md)。真实 IAM、ANI 界面、互联网、生产、正式存量迁移、容量/HA、Underlay 物理场景分别保持 `not_verified`。

## 第一批已交付：NET-VPC-BASE-01

状态：`completed_controlled`。本批 NET-U00、U01、U02、U03、U04、U08 的实现和所有必需受控门禁已完成。固定基线 `d8835a22d905e358b7f60756d3113baa97d7c762`，工作树 `/home/chabking/workspace/.worktrees/network-vpc-base-01`，分支 `codex/net-vpc-base-01`；实现验收时点的未提交成果已固定；用户随后授权提交并推送远端 main。详见[六卡交付与下一批输入](records/NET-VPC-BASE-01/README.md)、[最终成果 manifest](records/NET-VPC-BASE-01/source-manifest.json)和[输入审计](records/NET-VPC-BASE-01/candidate-audit.json)。

最终 ubuntu run `20260914T135236Z-18dc5c44`：`make verify`、固定生成/基线 breaking、真实 PostgreSQL 全量 race 均 **pass**，115 个顶层测试通过，9 个基础服务进程恢复场景通过。唯一可选容量测试未启动，不影响本批受控门禁；[逐项状态、命令及源码覆盖](records/NET-VPC-BASE-01/gates.json)保留 pass/fail/not_verified 层次，早期失败未覆盖删除。[清理复核](records/NET-VPC-BASE-01/cleanup.json)确认任务容器及运行进程已结束；其他工作树、共享缓存和原 LB 手工现场保留。

2026-09-14 用户在受控验收结束后授权将本批成果提交并推送远端 main；实现门禁与历史 manifest 保留原时点。此次 Git 发布不包含部署或真实存量补齐。第一批交付时 NET-U05—U07、U09—U12 均未启动；本批必要故障测试不等于 U07 全卡完成。真实内网流量、Public 出站及源地址、三类 LB、旧工作负载不中断、真实迁移/补齐均 **not_verified**。第二批以最终未提交成果和 0006/完整 LB 契约为输入，并先满足合格 kc/Envoy 镜像、拓扑、地址和独立数据面测试环境前提。

## 已交付设计输入：VPC 基础连接、公网出站与 LB

用户确认 EIP 只能绑定一个目标、SNAT 按内网/公网用途分别限制，并要求新方案和可执行任务。本次在 Network `d8835a22d905e358b7f60756d3113baa97d7c762` 上新建独立文档工作树 `codex/vpc-lb-plan-20260914`，交付[统一方案](../specs/vpc-connectivity-lb.md)、[NET-U00—U12 任务卡](../plans/vpc-connectivity-lb.md)与[ADR-0005](../adr/0005-separate-vpc-connectivity-and-exclusive-eip-bindings.md)。

设计交付时点状态：`design_delivered`。该历史文档轮次只修改文档，未实现新生命周期、未执行数据库迁移/存量补齐、未操作集群或 ANI/kc 源码，未提交/推送；当时 NET-U00—U12 均 `not_started`。当前实现与验收以本文件顶部当前工作为准。两条明确确认的规则与其余工程提案分开标识；基线、只读核对和文档检查见[交付记录](records/2026-09-14-vpc-lb-plan.md)。

历史手工 LB 三类型 HTTP 在 kind 成功，不能作为新产品 API、Public SNAT 出站、真实互联网或生产部署验收。下面保留已有 Public 分支的实际完成及外部阻塞；新设计不自动消除它们。

## VPC SNAT 已有实现基线

2026-09-10，按 [本轮 Goal](records/VPC-SNAT-IMPLEMENTATION/goal-objective.md) 在独立 `codex/vpc-snat-implementation` worktree 完成本仓实现与必要自动验证。固定基线 `e481e968d3cc2f17bc4c6a736c438428519b09a0`；设计输入 595 项及原有历史证据保留；实施结束时成果未提交，后续分支交付按下方新增授权执行。详细代码、命令、源码快照和边界见 [本轮实施记录](records/VPC-SNAT-IMPLEMENTATION/README.md)。

| 范围 | 当前结果 | 说明 |
|---|---|---|
| 平台出口、租户 EIP/SNAT 契约、持久事务、实际 kc adapter、同一 worker 持续观察 | `pass` | 新增领域/Proto/迁移、持久 namespace 与占用、UID/fencing、启停解绑释放、部分设备进度与依赖退化；受控接口与实际进程验证 |
| 最终 `make verify` | `pass` | ubuntu `20260910T145133Z-96a527b1`，exit 0 |
| 真实 PG 全量 race、故障恢复、VPC/Subnet/Attachment 回归 | `pass` | ubuntu `20260910T144842Z-acf48246`，exit 0 |
| 原生 Overlay 数据面 | `not_verified` | 合格 kc 修复版本与源码/运行镜像关联缺失，外部阻塞；本轮未执行 live 出网验收 |
| Underlay 自动测试 | `pass` | 实际 adapter + 受控事实/HTTP + 真实 PG；VLAN 0/非零合同、部分接管、占用、陈旧事实与生命周期 |
| Underlay 真实网卡/VLAN/物理出网 | `not_verified` | 按约定延后，未接管真实接口 |
| ANI Gateway/OpenAPI、真实 IAM、部署/发布 | `not_verified` | 本轮未接入或发布；新增出网 RPC 默认拒绝无可信调用上下文的请求 |

Goal 已标记为 `blocked`：本仓独立工作完成后，同一 Provider 前提连续三轮仍不满足；[第三轮只读复核](records/VPC-SNAT-IMPLEMENTATION/provider-continuation-03.json)确认源码、集群身份与镜像保持。恢复条件是合格 kc 版本、实际镜像 digest 及相关回归证据就绪。

完整 Goal **尚未完成**。按用户“你不用修复kc-networking的bug,标注就行了”，本轮只标注 serviceIP 热加载、跨 namespace EIP 候选及 Snat/VPC namespace 三项缺陷，不改 kc 或升级 CNI。[结束预检](records/VPC-SNAT-IMPLEMENTATION/provider-final-preflight.json)确认固定 kc 源码、原集群身份和运行镜像保持；历史手改 OVN 对照不作为原生通过。

重任务全部在 ubuntu 串行执行，无本地回退。所有任务 PG 容器已清理，未创建 live 测试资源或临时 NAT，未修改已有 VM/宿主防火墙。结束复核原有 13 类资源 UID、kcn-config 与节点路由保持；KUBE-SERVICES NAT 规则顺序差异单独记录，未回写，见 [环境证据](records/VPC-SNAT-IMPLEMENTATION/final-environment.json)与[差异](records/VPC-SNAT-IMPLEMENTATION/node-network-differences.json)。原设计 worktree、共享 checkout、历史迁移和证据保留；远端任务源码/构建物作为证据保留。

2026-09-11 用户确认 kc 未修复是既定事实，先保留该阻塞并处理后续流程。本仓交付核对已完成：[本次交接审计](records/VPC-SNAT-IMPLEMENTATION/handoff-audit-20260911.json)确认 140 项运行源码与最终两项远端门禁一致，595 项原设计输入及 588 项历史证据保持。保留原始门禁结果，不因相同源码而重复执行重测试或 kc 预检。

后续分开处理：本仓成果可继续交接；原生 Overlay 在合格 Provider 的固定源码、实际镜像 digest 与相关回归证据就绪后恢复。Underlay 物理验收按约定延期，ANI/IAM 接入仍属独立范围；用户随后选择本仓收尾及提交推送，目标为当前 `origin/codex/vpc-snat-implementation`；本次分支发布已获授权，见 [发布记录](records/VPC-SNAT-PUBLICATION-20260911/README.md)。其他工作包仍待各自明确范围。完整出网 Goal 保持 `blocked`，不将本仓交付核对视为原生出网通过。

## 历史工作与输入时点

以下为本轮实施之前的事实，不覆盖顶部 VPC SNAT 当前状态。

租户 VPC SNAT 方案文档已完成，包含 Overlay/Underlay、平台网卡/二层/网关/Public 池初始化、租户 EIP/绑定启停与释放、身份/持久化/删除保护及验收合同，见 [方案](../specs/vpc-snat.md)、[计划](../plans/vpc-snat.md)和[操作手册](../kc-public-egress-manual.md)。该方案交付时点仅整理文档和既有证据，当时 Network EIP/Snat 产品实现未启动，kc 修复未实施，Underlay 为 `not_verified`、后续再测；Overlay 保留下面的原始 fail 与诊断对照 pass。交付检查见 [方案文档记录](records/2026-09-10-vpc-snat-design.md)。

2026-09-10 用户授权的独立 kc Overlay EIP/Snat 出网测试已完成并记录，原始新建/重新启用后的出网为 `fail`：ER 源路由下一跳为空。仅对测试路由补齐下一跳后的双 worker HTTPS 对照为 `pass`，不替代原始失败。测试 CR 和临时节点 NAT 已清理，原有资源 UID、Pod/VM 状态、节点 NAT/路由与 kc 配置保持一致；kc 按需创建的空共享 ER 及系统连接端口保留，OVN 清单不完全等于测试前。详见 [本次实测与清理差异](records/KC-OVERLAY-20260910T114200Z/README.md)。本次没有修改 kc 源码或验收 Network EIP API，不改变下列工作包结果。

NET-05A 按用户调整后的范围完成，状态为 `completed_with_deferred_capacity`；1,000/2,000 容量未完成、延期，原完整容量矩阵仍为 `not_verified`。成果位于独立 `codex/net-05a` worktree；Goal 结束时保持未提交，后续提交推送授权见本页末尾；固定输入及证据见 [NET-05A 记录](records/NET-05A-implementation.md)。配额等待 Core 重构后独立接入。NET-05 历史验收及发布事实保留，持续观察豁免不适用于 NET-05A。

NET-05 已按用户调整后的范围完成，复用固定远端 `kind-kc062`，实际普通容器主链、带身份数据面、故障恢复及产品/临时环境清理均通过。独立 Network/ANI worktree、环境身份、用例断言和实际证据见 [NET-05 记录](records/NET-05-implementation.md)。验收完成后的新授权仅将 Network 提交至远端 main；ANI 继续保留本地。NET-06、部署和整体切换不在本次发布范围。

执行中用户将 worker 资源持续观察专项交由并行任务，本包停止扩展/重复该项；此前证据保留并标明源码时点。该调整不取消产品创建/删除、实例 owner 恢复和其他 NET-05 验收。

NET-02/03 实施与受控验收、NET-04 接口实现和进程验收已完成，见 [组合实施记录](records/NET-02-04-implementation.md)和[逐项审计](records/NET-02-04/completion-audit.md)。八条既有 compatibility 失败与全历史 Atlas 漂移仍单独保留，不由 NET-05 修改。

| 工作包 | 执行状态 | 说明 |
|---|---|---|
| NET-DOC | `completed` | 领域词汇、ADR、规格、计划、导航和检查记录已完成 |
| NET-ENV | `completed` | ubuntu 工具、PostgreSQL、镜像构建与源码快照远程 make verify 通过；无额外必需输入 |
| NET-01 | `completed` | VPC 纵向切片及受控环境/真实 PG 门禁完成；真实网络不由此项替代 |
| NET-02 | `completed` | Subnet CRUD/操作/地址约束/迁移升级/实际 adapter/持久恢复通过 |
| NET-03 | `completed` | 两端持久提交/封闭/释放与占用保护；真实 PG、十场景独立进程故障验收通过 |
| NET-04 | `completed` | 九路由/生成契约/HTTP链路与无新增兼容回归通过；8 条既有失败按用户决定单独保留，原门禁仍 fail；仅接口，无前端 |
| NET-05 | `completed` | 实际 main、V-12 连通/隔离/重叠、V-06–11 适用 live 扩展、V-13 接口、最终门禁和清理通过；持续观察专项按用户调整不再追加 |
| NET-05A | `completed_with_deferred_capacity` | 最终业务源码的适用门禁、V-16/17、V-18 功能及 100 单/双副本对照、V-19、新普通容器和清理 pass；1,000/2,000 容量未完成/延期 |
| NET-06 | `not_started` | NET-05A 新版本验收通过后再独立启动 VM/KubeVirt 接入与验收 |
| NET-AUTH | `not_started` | 明确延期：IAM 就绪后接入服务间身份验证 |

## 验证状态

执行状态与测试结果分开。`pass` 只用于实际执行并有记录的检查，未执行不等于失败或通过。

| 范围 | 结果 | 证据 |
|---|---|---|
| 当前源码/设计输入核对 | `pass` | [来源评估](records/2026-09-09-source-assessment.md)；仅静态时点 |
| 文档结构、链接、空白与一致性 | `pass` | [本轮检查记录](records/2026-09-09-design-verification.md)：独立审阅、18 份 Markdown、82 个本地链接、35 处源码行号 |
| 本轮通用运行骨架门禁 | `pass` | 本轮实际运行 `make verify`，exit 0；范围与结果见同一检查记录 |
| 远程开发环境与骨架门禁 | `pass` | [远程准备记录](records/2026-09-09-remote-readiness.md)；Go 1.26.7、Buf 1.60.0、sqlc 1.31.1、PostgreSQL 18.6；未回退本地 |
| NET-01 VPC、数据库、持久操作、实际 adapter 与恢复 | `pass` | [实施记录](records/NET-01-implementation.md)：V-01 及 V-02/03/04/06/07/08/09/11 的 VPC/受控范围 |
| NET-02 Subnet / NET-03 两端 Attachment 协议 | `pass` | [组合记录](records/NET-02-04-implementation.md)：真实 PG、受控 Provider 与实际服务进程；V-02–11 适用范围 |
| Gateway 九路由与普通容器 owner 接口链 | `pass` | V-13 仅接口范围；真实双页游标、租户、幂等和错误映射；十场景进程故障矩阵 |
| ANI Core compatibility | `fail` | [固定基线差异与用户决定](records/NET-02-04/baseline-gate-drift.md)：8 条既有路由，不属于本次 Network breaking 预期 |
| ANI 全历史 Atlas 目录 | `fail` | 固定基线 checksum 漂移及重复版本；本包新迁移 SQL/角色测试 pass，不替代全目录重放 |
| NET-05 真实 kc/OVN 数据面、故障及清理 | `pass` | [完整矩阵](records/NET-05-implementation.md)：八 Pod/两 worker、40 个带双域正向控制的隔离负例、真实恢复和产品清理；50 项 fixture 撤销、原环境 250 项 inventory 保留 |
| NET-05A 功能、真实普通容器与清理 | `pass` | [验收矩阵](records/NET-05A-implementation.md)：最终 67 文件源码；11 项门禁保留两类原 fail；九 Pod、177 流量断言、65 项 fixture 撤销、344 项原 inventory 保留 |
| NET-05A 100 Attachment 单/双副本对照 | `pass` | [最终容量判定](records/NET-05A/stages/20260910T083645Z-6386a4b5/capacity-evaluation.json)：新版 p95/p99 与最坏 60 秒预算通过；固定旧版延迟失败保留 |
| NET-05A 1,000/2,000 容量 | `not_verified` | 远端 load 保护中止后，按用户授权未完成/延期；不缩数据集、不放宽阈值，不作为大规模容量通过 |
| Console/前端 | `not_verified` | 不在本 Goal |
| VM 网络 / IAM 服务间验证 | `not_verified` | V-14、V-15 后续独立验证 |
| 生产发布、部署或切流 | `not_verified` | 不在本轮范围 |

## 历史分支提交与当前边界

NET-02–04 后续发布记录属于历史输入：Network 固定提交已发布；ANI 固定提交仅保留本地，用户最新决定暂缓推送。NET-05 原 Goal 的验收阶段只允许隔离验收与远端临时验证提交；验收结束后用户明确授权发布 Network 至远端 main，并再次确认 ANI 继续保留本地。KC-KIND 后续记录作为单独环境输入，不混入固定业务源码。

## 历史下一步安排

NET-05A 按用户调整后的范围结束并停止；Goal 结束后的独立提交推送按本页末尾授权执行。1,000/2,000 容量未完成，后续在资源条件具备且独立恢复该工作后验证；不自动启动 NET-06、配额或发布。以下 NET-05 发布说明为历史安排。

NET-05 验收完成，按用户后续授权发布 Network main，ANI 成果继续保持本地未提交状态。NET-06、NET-AUTH、Console、生产发布/升级和整体切换等待各自独立授权；不自动继续。

## 记录索引

- [2026-09-09 来源评估](records/2026-09-09-source-assessment.md)
- [2026-09-09 设计文档验证](records/2026-09-09-design-verification.md)
- [2026-09-09 远程环境准备](records/2026-09-09-remote-readiness.md)

- [NET-01 实施与验证](records/NET-01-implementation.md)
- [NET-05 实施与真实环境验收](records/NET-05-implementation.md)

## Network main 发布授权

2026-09-10 用户在验收与清理结束后明确要求提交远端 main，并确认仅发布 Network，ANI 继续保留本地。发布从 `87e91de53aff9158a0525950c59de8a338f01ccc` 建立独立 worktree；该提交是 NET-02–04 的等价 squash，其 tree 与固定 `5d4a534` 完全相同。发布范围为已验收 NET-05 成果和本段授权记录，原验收工作树及封存证据保留。提交前校验完整发布树的 verify/audit/SBOM；远端实际提交及 CI 状态以 GitHub main 与对应提交 checks 为准，不将本地门禁冒称云端 CI。

发布快照按已校验清单显式暂存文件，保留本次明确纳入 Git 的脱敏日志，避免默认 `*.log` 忽略规则使远端验证/SBOM 输入少于实际发布树。该修复只影响源码快照传递，不改变业务运行行为。

## NET-05A 后续提交推送授权

2026-09-10，用户在 NET-05A Goal 关闭后明确要求提交并推送 Network service。目标为 `origin/codex/net-05a`，包含已验收的实现、迁移、测试运行器、文档和脱敏证据；原 Goal 完成记录中的“未提交”描述保留为当时事实。本次授权不包含其他仓库、NET-06、配额、大规模容量补测、registry 镜像发布或整体切换。

提交前以完整待提交文件清单在 ubuntu 单条受限流水线运行 `make verify`、`make audit`，为包含脱敏证据的实际提交内容重新生成 SBOM。验收阶段排除原始证据的源快照与本次提交内容分别标识；精确远端提交和对应 CI 结果以远端分支与该 SHA 的 checks 为准，不用临时验证提交冒充发布版本。

## VPC SNAT 后续分支提交推送授权

2026-09-11 用户明确选择完成本仓交付收尾与提交推送，目标为 `origin/codex/vpc-snat-implementation`。[发布记录](records/VPC-SNAT-PUBLICATION-20260911/README.md)保留完整暂存树、ubuntu 门禁、历史证据的精确属性处理和最终 SBOM 流程。首轮 `make verify`、6 项 tenant-mutations、漏洞/密钥/SBOM/notice 门禁 `pass`；原全量 PG/race 对应的 140 项运行源码仍保持。当前交付分支的真实提交和 exact-SHA CI 以 Git/托管平台为准，不将临时验证提交冒充发布版本。

本次源码交付不解除 kc 外部阻塞，不宣布原生 Overlay 出网通过。Underlay 物理、ANI Gateway/IAM、合并 main、PR、镜像发布和部署继续保持各自边界。

## Image MVP

Run `img-20260929T1718Z`; review branch `codex/image-mvp-20260930`. [Baseline and bindings](records/IMAGE-MVP/img-20260929T1718Z/README.md). Local edits/Git/transfer only; all generation and validation Fedora. This section is the only current Image ledger. Network history above retains its original scope.

| Task | code | isolated | live | product | Evidence / next action |
|---|---|---|---|---|---|
| IMG-00.1 | pass | pass | n_a | n_a | Fedora preflight exit 0, pinned tools and 274 document link targets |
| IMG-00.2 | pass | pass | n_a | n_a | Fedora preflight exit 0, pinned tools and 274 document link targets |
| IMG-00.3 | blocked | not_run | blocked | blocked | frontend found; consumer/namespace owner unconfirmed |
| IMG-00.4 | pass | pass | n_a | n_a | Fedora preflight exit 0, pinned tools and 274 document link targets |
| IMG-00.5 | n_a | n_a | blocked | blocked | approved live profile, Harbor version and TLS binding missing |
| IMG-00.6 | pass | pass | n_a | n_a | Fedora preflight exit 0, pinned tools and 274 document link targets |
| IMG-01.1 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-01.2 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-01.3 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-01.4 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-01.5 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-01.6 | pass | pass | n_a | n_a | [IMG-01 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-01.md); contracts/pure rules only |
| IMG-02.1 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-02.2 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-02.3 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-02.4 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-02.5 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-02.6 | pass | pass | n_a | n_a | [IMG-02 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-02.md); exact SHA 7e4069d, true PG + race + mutations + verify |
| IMG-03.1 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-03.2 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-03.3 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-03.4 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-03.5 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-03.6 | pass | pass | blocked | n_a | [IMG-03 evidence](records/IMAGE-MVP/img-20260929T1718Z/IMG-03.md); exact SHA c17d64f, race + verify; live profile missing |
| IMG-04.1 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.2 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.3 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.4 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.5 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.6 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-04.7 | running | not_run | blocked | n_a | tenant space and credential recovery; approved live profile missing |
| IMG-05.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-05.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-05.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-05.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-05.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-05.6 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-06.6 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-07.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-07.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-07.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-07.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-07.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-08.6 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-09.6 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.5 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-10.6 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-11.1 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-11.2 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-11.3 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-11.4 | not_run | not_run | not_run | not_run | dependency gate pending |
| IMG-11.5 | not_run | not_run | not_run | not_run | dependency gate pending |

IMG-00 n_a: baseline/import tasks have no product/live behavior. Next: IMG-04 persisted space and credential recovery; missing live/consumer inputs do not block backend independent tasks.

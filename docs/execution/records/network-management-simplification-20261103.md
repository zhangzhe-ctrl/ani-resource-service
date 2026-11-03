# 2026-11-03 网络管理简化实施证据

范围：Resource 与 Console；不修改 KCN。完整主链为网络创建、消费者接入/流量、消费者退出与网络真实清理。方案见[实施方案](../../plans/network-management-simplification-20261103.md)，当前结果仅在[执行状态](../status.md)汇总。

## 固定来源与环境

- Resource 基线 `b35365d7e459aad55edefde4270723157c29ea26`，独立分支 `codex/network-management-simplification-20261103`。
- Console 基线 `2e6f278111bc169ed2df7f480304093a11528e0b`，独立分支 `codex/network-observation-ui-20261103`。
- Fedora 任务根 `/home/chabking/tasks/network-management-20261103`；原仓库及其他任务工作树保留。Go 1.26.10、sqlc 1.31.1、buf 1.60.0；PostgreSQL 测试使用脚本独占临时容器。
- 10 集群 kube-system UID `5277649d-e28d-4a0a-ae76-8354365c63bc`，12 集群 `3eeb5379-8748-4bf8-8f6d-d7a48ce3bc24`。12 集群经本机 SSH 转发，驱动/测试仍在 Fedora；登录使用正常租户/平台 API，凭据只存任务 private 目录。

## 修复前事实

本次诊断的 300 秒固定窗口：12 集群 GET/LIST 6000 次（3691/2309），10 集群 4728 次（2241/2487）；两环境分别比较，不相互当作基线。12 集群 11 条 Deleted 仍调度，另 6 条未发送且封闭的取消已退休；还存在 4 条平台 Deleted 和 24 条 released Attachment 周期调度。两个历史 VPC 缺少 Provider UID；另一个 SNAT 有残余 RESOURCE_IN_USE，不能直接清 reason 冒充完成。

10 集群捕获同一公网 LB 原 operation 的 Observe deadline 后退化，随后原身份恢复；采样时原生 Ready/HA 保持。此证据说明 Res 观察路径可产生退化，不证明所有历史 NOT_READY 都有同一原因。#47 原 EIP 容量恢复缺少 KCN 重试，本次不修改或关闭它。

原始窗口：12 集群 `/home/kubercloud/network-issues-readonly-20261103-0145/window-48-02`；Fedora `/home/chabking/tasks/network-issues-readonly-20261103-0145/window-lab-02`。

## 可复现行为验证

- RED：`red-lb.log`，读取超时/临时失败错误地把 Available 改成 Degraded。
- PASS：`fifth-pg.log`，真实 PostgreSQL + 真实业务/Provider adapter，Kubernetes HTTP/实例 owner 外部边界受控。覆盖 VM 旧投影当前准入、同名替换 UID 拒绝、VM Attachment 退出、终态退休和原对象事件恢复、通知租约围栏、普通 LB 不启动全量采集、关键操作两次 410 relist 后仍完成当前证明、安装检查迁出及原生 False 条件仍退化。
- Console 格式化、lint、类型检查及 GitNexus 结果位于同任务根；图谱 UNKNOWN 调用点通过实际 import/路由确认，没有把零边当作无影响。
- `make verify` 第一轮因旧故障注入脚本锚点失效而 FAIL；同步更新真实审计入口的注入点后重新执行。不得将此前 PASS 冒充最终源码通过。

## 保留的必要边界

消费者关闭与实际网卡/IP 释放、SNAT 删除后的原 EIP 解绑、LB 生成对象规格/HA/身份仍保留当前确认。安装诊断独立执行 `go run ./scripts/lb-api installation --verify ...`。没有通过调整 QPS、Burst、展示 TTL 或修改 KCN 来制造收益。

真实环境业务验收、发布镜像与 300 秒后窗口在完成后追加到本记录；受控测试通过不代表真实验收完成。

## 发布前回归补充

- 并发准入曾 FAIL：12 个 Attachment 请求占满 8 连接池，持父行锁的事务再次借连接读取 Namespace 身份。事务内 Provider 读取现复用当前查询连接；`TestAttachmentPermanentIdentityTenantScopeAndParentDeletion` PASS。
- WatchList 的 `sendInitialEvents=true` 与 LIST 同属重建边界，普通 Watch 续接不失效。`watchlist-fix.log` 3 项 PASS（8.426s）；`egress-relist-final.log` PASS（4.790s）。普通观察等待共享视图，关键清理保留重连后的完整证明。
- `admission-final.log` 的当前原 CR UID/Ready/代次/未决操作拒绝、旧投影不改时间准入、离线保存/激活均 PASS；其中一项父删除并发测试在夹具创建第二个 Subnet 时因父基础连接投影未收敛 FAIL，夹具改为每次创建前驱动真实父 worker 到 Available，未修改业务父删除保护。
- `attachment-lb-final.log` PASS（105.399s）：父删除并发、后端证据年龄/未来时间/UID 围栏，以及真实双进程创建结果不明、更新响应丢失、删除响应丢失、未知创建期间删除四场景。Attachment 读取暂时失败只标记 ProviderUnavailable；LB 仍要求未过期的已登记身份与当前原生对象匹配。
- 扩大的 `network-regression-final.log` 不是 PASS：旧投影预期失败已定向修正复核，但整组在 6 分钟限时退出，未完成部分明确未验证。后续不把此日志作为完整网络集成套件通过。
- Console 提交 `939d643`，PR https://github.com/bykenkk/ani-console/pull/11；代码验证通过，未生产构建/部署。
- 12 集群 `ani_resource` 已完成 PostgreSQL custom-format 备份及服务端 TOC 检查；迁移尚未执行。这里的 network schema 25→27 属于 Res 数据库，非 KCN。

- 最终 `verify-release.log`：PASS，含生成一致性、边界、故障构建、Go 单元测试/vet/build/module 校验。重任务均在 Fedora 执行。

## R1 真实验收失败与回退

2026-11-03，`2fc544f` 镜像在 10 集群滚动更新完成，原三 LB 四入口首次 HTTP 通过，但随后 Attachment 观测时间持续老化，LB 出现 BackendIdentityMismatch 并退化。已只回退 Res 镜像至原 `sha256:4c651e6e...`，原三 LB 同 operation/版本恢复 Available；未修改 KCN，12 集群尚未迁移或更新。

受控复现发现父/平台通知可提高 Attachment 的 requested_generation，但其 Pod/NIC/IP 事实并未变化；普通路径因此一直拒绝未改变的共享视图。修正仅让关系未变、来源 epoch 有效的普通视图覆盖这种通知，不改变 collected_at；初始化、消费者关闭、相关对象变化、UID 替换仍保留当前收集与 fencing。`hint-coverage.log` 通过 11.910s，包含旧时间不续签、真实关系变更拒绝、relist 与领取/观察/完成各阶段通知围栏。

用户追加要求：12 集群的最终镜像改在 `ubuntu@100.64.128.15` 从固定源码重新构建并推送，再更新 12；Fedora 保留测试和 10 集群发布职责。

## R2 真实回归及后续收敛

`b9bd56e` 在 Fedora 和 15 分别构建，源码快照均为 `sha256:2482896381b041384b80f0b8174eacb2569b6dd408e834cde4864057cd487522`。15 已直接推送仓库，但未更新 12。10 的 300 秒窗口仍 FAIL：原 LB 发生状态退化；GET/LIST 为 5179 次，虽 LIST 降至每类约 33–34 次，普通身份读取仍过多。Attachment 能续新，但 Service 的共享 owner UID 通知仍反复唤醒它；LB/其他普通观察仍将这种未改变事实的持久通知当作需要重新采集。10 已只回退 Resource 镜像，原 LB 身份保留。原件 `live/lab/window-r2-failed/`、`r2-rollback.json`。

确定性 LB 回归 `lb-hint-red.log` FAIL、`lb-hint-green.log` PASS（29.708s）：相同对象与时间的共享证明覆盖普通通知，同时真实 Pod 变更仍拒绝旧视图、关键路径仍通过两次 relist。`attachment-hint-filter.log` PASS（9.410s）：共享 owner UID 的 Service 事件不再制造 Attachment 工作，Pod/NIC/IP/EIP 变化及租约各阶段通知保护保留。后续将同一普通观测规则用于 VPC/Subnet/Egress 和已确认删除记录；停用终态的无关平台唤醒，LB 仅绑定直接 VPC/Subnet/EIP 及自身生成对象/成员事件，去掉已不用于运行判断的底层平台依赖通知。

该现场还明确了另一条误判：`lbMemberIdentity` 将 Attachment 证据过期与实际 UID/归属不匹配都折叠成 `false`，触发 backend 撤出及 LB degraded。现区分这两种结果：当前管理身份/原生 Pod→VNic→VNicIP 地址链不符仍拒绝；身份链吻合但证明时间不足或 DB 读取失败时返回观测异常，保持最后结果与原时间，操作仍等待确认。没有延长 TTL、把旧时间改成当前时间或放行未知 backend。

R3 发布前 Fedora：`r3-related.log` PASS（38.863s），`r3-backend-observation.log` PASS（31.895s，含真实 Worker/PG 保留配置与观测时间、future/expired 证据不可采用、过期情况下真实 UID 替换仍拒绝），最后 `r3-final-verify.log` 完整 `make verify` PASS。后续真实环境结果见下节。

## R3 发布与真实环境结果

业务源码固定为 `dbe3cd514a3cedb4f6ab1474ddc6906bb0be8440`，两个构建主机使用相同源快照 `sha256:7a7c37a9a9a22e601300775fe8b6a6339900d20b2ebe8cbb7f5406ea99a24f1b`。

- 10 集群在 Fedora 构建并发布：`172.16.101.10:30003/icn-01a15337-release/ani-resource-service@sha256:fd46eaed0213d21e504d5fd4dd7c6fdb20f4ddf54316a0f1588ed0c54ace7abf`。
- 12 集群按用户要求在 `ubuntu@100.64.128.15` 重建并推送：`10.10.1.66:30003/ani-gpu-platform/ani-resource-service@sha256:bdd19ba3317f2b39859b72b9894c2513d514cc89dfb1dc2d6fae37a4e0986e93`。两个 Deployment 均只替换镜像，原 UID 保留，滚动更新完成；QPS 20、Burst 40、30 秒 audit 和 60 秒 stale 参数未调整。
- 12 的 Resource `ani_resource` Network schema 从 25 正常迁移至 27，使用正式 `-migrate` 入口；迁移文件与 R3 一致。迁移前 custom-format 备份与 TOC 已核验，备份 SHA-256 `c4229acd454f2498c80150e509ab320213bdd210361114a40d16b015683d5a69`。这是 **Resource 数据库迁移，不是 KCN schema**。回执为 `live/full/migration.json`；原 25 版镜像不能直接回滚到 27 库。
- KCN 源码仍为 `6132623baabee50f17e9e179c0f5e9c1f380db04`，工作树干净；没有修改 KCN 代码、镜像、配置或 CRD。

10 集群 `live/lab/window-after/`：连续 300 秒、21 次指标采样、11 次投影采样，同一 Pod/container，采样全部成功。GET/LIST **1767 次，5.89 次/秒**，相对原同集群 4728 次下降 **62.6%**；基础 GVR 各 23 次 LIST。无传输错误计数增量、audit failure、watch error、queue overflow，也无窗口内 limiter/deadline 错误。此窗口仍有两个既存活动 VPC 的真实 ownership conflict 和平台 egress 观测待确认，不能称所有网络资源已健康，也不把 23 次 LIST 全部归为 30 秒定时器。

4 个 LB 的 44 个投影样本全部 Available，同 operation/配置版本；启动恢复阶段短暂 ProviderUnavailable，保留旧时间与配置，最大年龄 71.77 秒，随后正常续新。最终三条主样本 fresh/available/configured；原 **15 个 Gateway/Deployment/Pod/Service UID 全部保留**。观测前后私网、公网、双模式的 **四入口 HTTP 200 + 正确 marker 均 PASS**，没有重建 LB 或修改 backend/operation。回执 `r3-final-invariants.json`、`r3-final-lb-snapshot.json`、`r3-final-*-private-http.json` 和 `external-curl/r3-final-*.json`。本结论覆盖已复现的读失败/证明滞后误判，不追认所有历史 NOT_READY 的原因。

12 集群使用正式 tenant-a API 新建 VPC `vpc_9913cae07ff54ce5a15a26263dfbb843`、Subnet `subnet_d3f07993e4f945ea939c5d69b5fa18ac` 和 VM `7aef1e31-0cc3-44a1-a30d-0a53007a0b51`：

- VPC/Subnet Available，基础出口 Ready；VM 创建 Running，客机取得 `10.131.1.2/24`、默认网关 `10.131.1.1`，串口内真实 ping 网关 3/3 成功。
- 客机内执行正常关机，串口记录 `reboot: Power down`，Resource 独立观察为 Stopped；desiredState=Running、version=1、原创建 operation 保持。随后正式平台 Start→Running/version2、Stop→Stopped/version3、Delete→Deleted 完成。没有网络过期前置拒绝。
- 原 VM UID `1327ac91-dfdb-43d9-a688-34f264605eb2` 保留至删除。两个启动周期共捕获 12 个原生 UID：VM×1、VMI/launcher Pod/VNic/VNicIP 各×2、DV/PVC/PV 各×1；删除后逐项 GET 均 absent。
- 正常删除 Subnet 和 VPC 完成；原 VPC/Subnet、基础 EIP `eip_2e8b9ec814be40d291baaf4f82e906bb`、SNAT `snat_42542a137e724234b98473b4f0f89907` 全部 absent，对应网卡/IP/NAT/Service 引用为空。共享 NAD `ani-vm-primary-v1` 原 UID 保留。该 VM Attachment `att_126daeb7b45c4f23819d50461e0106a8` 为 released、reason 为空、`next_check_at=infinity`、无 protocol block。

R3 业务回执位于 `live/full/r3-business/`：`vm-flow.json`、`vm-first-running.json`、`guest-console-probe.json`、`guest-console-shutdown-final.json`、`vm-guest-stopped.json`、`vm-after-{start,stop,delete}.json`、`vm-native-cleanup.json`、`network-native-cleanup.json`。HTTP 400 的原始驱动失败保留：VM 幂等键要求 UUID、SSH 公钥不能带换行、Delete 不接受 force-stop 专用确认字段；修正验收请求后复用有效幂等键，没有改产品代码。串口首轮提示/ANSI 解析错误也保留，客机 ping 的原始完成输出已重新解析，没有重复业务操作冒充首次通过。辅助 SSH 端口转发曾报 no route to host，未修复该通道；本次内网与客机关机依据实际串口结果，不把 SSH 记为 PASS。

## R4：停止 VM 的预留连接

R3 的 12 集群完整 300 秒窗口为 1606 次 GET/LIST，较基线减少 73.2%，每类基础 LIST 19 次；无传输错误/limiter/deadline。25 条 Deleted 和 25 条 released Attachment 均不再周期调度，两个缺 UID 的历史 VPC 保留 ownership conflict 且 epoch 不增长，原残留 reason 的 SNAT 经真实观察完成确认。7 个现存 VPC 均 Available、观察及时；原 5 台 VM 的 11 组采样状态/版本/operation 不变。窗口保留于 `live/full/window-r3-before-stopped-fix/`。

该窗口尚未满足成本收敛目标：既存停止 VM `e739fb1a-4921-472d-baa3-743616aa7c42` 的 `att_f07dd6c001e24641a7b7e261433c9f57` 为 Reserved、reason 为空，却仍使用独立定时器和首次接入采集分支。本轮没有操作该受保护 VM；修复仅让已接受 open/stopped 快照的 VM 预留连接按普通共享观察运行，保留占用。活动首次接入、封闭后释放和协议恢复继续当前证明。

真实 PG + Provider/Worker 回归 `r4-stopped-red2.log` 确定性 FAIL（Stopped Reserved 仍设置 timer），`r4-stopped-green.log` PASS（9.220s）：停止后停放、父通知不重采或续签旧时间、最终释放仍新采集；另覆盖 VM 重新接入/原生 UID、未登记 launcher 拒绝、封闭释放及两次 Watch relist 清理。首个 RED 夹具遇启动观察待同步，补入真实初始 audit 后才得到上述目标失败，没有更改业务断言。

R4 最终 `r4-final-verify.log` 完整 `make verify` PASS，生成一致性、边界、故障构建、单元测试/vet/build/module/diff 检查全部完成；三份 Go 文件与本地交付源的 SHA-256 一致。随后只补充文档和现场回执，不重新扩大测试范围。

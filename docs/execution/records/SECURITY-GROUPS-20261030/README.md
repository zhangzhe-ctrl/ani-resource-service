# 安全组实现与固定验证（2026-10-30）

本记录是本轮命令与时点证据；当前进度唯一入口为[执行状态](../../status.md#security-groups-20261030)，规则合同见[租户安全组](../../../specs/security-groups.md)。

## 来源与执行

- Resource 从 main `a4bb202f2a85d8d69e83df269dc63c928fcd57fb` 开始，交付前合入并保留并发 main #29 的 `9fc934efee8cab1764bcdf6800f8a19322397dd6` 列表合同；全部重任务经 `ssh fedora`，任务目录 `/home/chabking/task-runs/network-security-groups-20261030`，串行使用既有 `net05a-heavy.lock`。
- 最终受测 API/cmd/internal/migrations 与 Go/Buf/sqlc 配置共 694 文件，本地与 Fedora [runtime-source.sha256](runtime-source.sha256) 完全一致，清单 SHA256 `7409869d37ad93f4c54aca52ee0e381b291eb38ebbd936e9b573c4c733b9fcac`。随后仅补部署 RBAC、规格、状态与本证据记录。
- Go 1.26.10、Buf 1.60.0、sqlc 1.31.1；PG 使用固定 digest 的临时容器、独立数据库与受限角色。未拷贝凭据到源码。
- KCN 源码固定为 `6132623baabee50f17e9e179c0f5e9c1f380db04`，未修改。目标 API `172.16.101.10:6443`；节点仅 `.10/.11/.12`，UID/地址在写前验证。实际镜像 digest 见 [identity.json](live-sg-07/identity.json)，镜像与本地 commit 的构建对应关系未证实。

## 真实命令和结果

| 执行 | 结果与边界 |
|---|---|
| `make generate`；最终 `make verify` | PASS：固定生成、领域边界、fault build、tidy、全部普通单测、vet、build、module verify、diff check；[合入最新 main 后最终日志](resource-verify-11.log) |
| `scripts/integration -timeout 10m -run '^TestSecurityGroupsPostgres' -v ./internal/data/network` | PASS：真实 PG 的规则/永久重放/租户隔离/默认行保护、consumer revision/引用释放、未绑定 EIP 版本与删除保护；[日志](resource-pg-04.log) |
| `INSTANCE_TEST_RUN='TestSecurityGroupsPersistent|TestInstanceSecurityGroupsVertical' scripts/instance-integration ./internal/data/instance` | PASS：真实 Instance/Network/Image PG 与 Service/worker，创建选组→热更→清空→删除；热更后三轮后台调谐仍保持 Pod UID；仅 Kubernetes/Controller/CNI 是外部边界替身；[日志](resource-instance-vertical-09.log) |
| `NETWORK_JOINT_TEST_RUN='^TestNetworkJointHTTP/security_groups$' scripts/network-joint-integration` | PASS：Gov HTTP JWT/AK、Casbin、套餐/租户→Resource mTLS→真实 PG/worker，规则版本、EIP waiting→applied、引用删除保护、清空及产品补偿；SG 子场景 33.96s，整体 41.65s；仅 Kubernetes/KCN 外部边界受控；[日志](governance-joint-sg-02.log)。此轮联测用任务独立 `joint.work`，不代表正式 SDK pin。 |
| `NETWORK_SG_LIVE=1 ... scripts/integration -timeout 8m -run '^TestSecurityGroupsKCNLiveLifecycle$' -v ./internal/data/network` | PASS：真实 Resource UC/PG/worker/KCProvider→授权集群，SG 创建→规则更新→删除；[日志](resource-live-sg-07.log)及[清理](live-sg-07/cleanup.json)。未模拟 KCN，未改共享服务。 |

真实 KCN 的 [创建](live-sg-07/create-ready.json)与[更新](live-sg-07/update-ready.json)均确认 Valid/Ready、observedGeneration 追平，原 UID 保持，generation 1→2；仅默认 ingress all/drop priority 1，egressRules 为空，组内互信关闭。删除通过产品 UC 和正常 finalizer 完成，随后任务 namespace 消失。

## 修复和未验范围

首轮 Network 编译的私有 helper 引用错误已修；PG 检查发现 Provider 名下划线及 nil KeepIds 不释放引用，已修复并通过真实 PG。容器纵向测试先后暴露缺可信 caller、缺 Image Runtime 与受限角色的 fixture 装配问题，保留生产身份/镜像 guard 后通过；热更同步管理版本，防止下一轮调谐触发额外生命周期变更。失败原日志保留在任务目录，不将中断运行计为 PASS。

Console 代码已落在 Fedora 原 `feat/governance-api@ad70a4f2` dirty 工作区，原 VM/存储改动保留；功能补丁 SHA256 `ce4ceb487418fb280748d910e21cf034ab4ebffea5ec5d2b04655ea34c1f3a20`。lint/格式/typecheck PASS；GitNexus 检测有明显索引错链并返回 critical，未记 PASS。未生产构建、未操作 dev。

共享 Resource/Gov 部署、权限目录/租户 A 管理员角色授权与 RBAC 应用：PASS，见下文。真实 EIP/VNic 成员规则与包过滤、真实容器/VM 全业务创建及浏览器验收：NOT_RUN。真实 SG 生命周期与受控 EIP/容器链不能替代这些验收。

合入最新 main 的列表合同后，全仓 tidy 暴露两份历史 Go overlay 被当作当前 module 源码扫描。归档内容原字节保留，文件名更为 `.go.txt`，复现脚本改读文本；历史 JSON/日志中的原执行路径保持时点事实，重放旧命令时先从文本恢复原输入文件名。该归档修复不引入旧 Inference/Model SDK 运行依赖。

## 10 集群部署与真实租户链（2026-10-30）

用户明确授权更新服务后，在 Fedora 构建并通过正常 Harbor TLS 发布固定提交：Resource `84d2cee378106a4cb7d0fbb3fc765c50e07c4120`、Governance `6cb83f385ce7d2b66e40004c0c1394ad530d73bf`。Gov 正式 SDK 为 `v0.0.0-20261030064014-84d2cee37810`，无 replace/go.work。目标 API `172.16.101.10:6443`，写前核对 cluster UID `5277649d-e28d-4a0a-ae76-8354365c63bc`、三节点 `.10/.11/.12`；[最终回执](deploy-10/deployment-result.json)绑定两实际镜像 digest、源提交、Pod UID、Ready 与 restart=0。

先备份 Network DB 和 Gov 目录/授权表到 Fedora 任务私有目录，再执行 Network 0023 [迁移](deploy-10/sg-network-migrate-84d2cee.log)、补齐 Resource 精确 SG/EIP/Pod/Notebook/VM patch [RBAC](deploy-10/rbac-result.json)，仅替换两个 Deployment 镜像，保留其他 Pod 运行配置。Gov `sync-apis --dry-run` 审核后同步目录，再执行仓库 SG [权限登记 SQL](deploy-10/permissions-sync.log)，不删除旧 API。首次真实 tenant-a/admin 查询403；只为原有 NETWORK/INSTANCE/VIRTUAL_MACHINE 套餐及对应管理权限的 tenant1/role3 `tenant:manager` 增加15项本功能权限，原DENY/OFF冲突则拒绝，未改其他角色或套餐；[回执](deploy-10/tenant-a-permissions.log)。Gov 新Pod重载策略后同一真实会话查询200。

真实 HTTP JWT→Gov鉴权/套餐→mTLS→Res PG/worker→现有 KCN [全链 PASS](deploy-10/smoke-result.json)：创建 SG 并等 available、只读默认行删除400、增加 TCP/22 allow priority10、删除自定义规则、删除 SG、KCN对象消失；[原始接口日志](deploy-10/smoke.log)。[创建读回](deploy-10/smoke-kcn-created.json)只含 ingress all/drop priority1、egressRules=[]、组内互信false；[更新读回](deploy-10/smoke-kcn-rule-added.json)同UID且generation/observedGeneration均2、Valid/Ready=True。测试驱动最初等DELETE后404，而产品保留deleted快照；更正驱动为deleted终态并独立核实KCN对象已无，不改产品行为。测试SG已清理，未操作已有业务资源，KCN未修改。

Console仍运行于 Fedora原 `feat/governance-api` 工作区，`http://172.16.101.31:5173/security-groups` 返回200，代理继续连接已升级的10集群Gov `:30085`。完整浏览器操作、真实EIP/VNic成员及流量、真实容器/VM新建/更换仍NOT_RUN。

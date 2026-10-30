# 安全组实现与固定验证（2026-10-30）

本记录是本轮命令与时点证据；当前进度唯一入口为[执行状态](../../status.md#security-groups-20261030)，规则合同见[租户安全组](../../../specs/security-groups.md)。

## 来源与执行

- Resource 基于 main `a4bb202f2a85d8d69e83df269dc63c928fcd57fb`；全部重任务经 `ssh fedora`，任务目录 `/home/chabking/task-runs/network-security-groups-20261030`，串行使用既有 `net05a-heavy.lock`。
- 最终受测 API/cmd/internal/migrations 与 Go/Buf/sqlc 配置共 693 文件，本地与 Fedora [runtime-source.sha256](runtime-source.sha256) 完全一致，清单 SHA256 `71d494a161227dcc5c4573841951bd0ffaeb4aaa3405ce9c40472910558b313f`。随后仅补部署 RBAC、规格、状态与本证据记录。
- Go 1.26.10、Buf 1.60.0、sqlc 1.31.1；PG 使用固定 digest 的临时容器、独立数据库与受限角色。未拷贝凭据到源码。
- KCN 源码固定为 `6132623baabee50f17e9e179c0f5e9c1f380db04`，未修改。目标 API `172.16.101.10:6443`；节点仅 `.10/.11/.12`，UID/地址在写前验证。实际镜像 digest 见 [identity.json](live-sg-07/identity.json)，镜像与本地 commit 的构建对应关系未证实。

## 真实命令和结果

| 执行 | 结果与边界 |
|---|---|
| `make generate`；最终 `make verify` | PASS：固定生成、领域边界、fault build、tidy、全部普通单测、vet、build、module verify、diff check；[最终日志](resource-verify-09.log) |
| `scripts/integration -timeout 10m -run '^TestSecurityGroupsPostgres' -v ./internal/data/network` | PASS：真实 PG 的规则/永久重放/租户隔离/默认行保护、consumer revision/引用释放、未绑定 EIP 版本与删除保护；[日志](resource-pg-04.log) |
| `INSTANCE_TEST_RUN='TestSecurityGroupsPersistent|TestInstanceSecurityGroupsVertical' scripts/instance-integration ./internal/data/instance` | PASS：真实 Instance/Network/Image PG 与 Service/worker，创建选组→热更→清空→删除；热更后三轮后台调谐仍保持 Pod UID；仅 Kubernetes/Controller/CNI 是外部边界替身；[日志](resource-instance-vertical-09.log) |
| `NETWORK_JOINT_TEST_RUN='^TestNetworkJointHTTP/security_groups$' scripts/network-joint-integration` | PASS：Gov HTTP JWT/AK、Casbin、套餐/租户→Resource mTLS→真实 PG/worker，规则版本、EIP waiting→applied、引用删除保护、清空及产品补偿；SG 子场景 33.96s，整体 41.65s；仅 Kubernetes/KCN 外部边界受控；[日志](governance-joint-sg-02.log)。此轮联测用任务独立 `joint.work`，不代表正式 SDK pin。 |
| `NETWORK_SG_LIVE=1 ... scripts/integration -timeout 8m -run '^TestSecurityGroupsKCNLiveLifecycle$' -v ./internal/data/network` | PASS：真实 Resource UC/PG/worker/KCProvider→授权集群，SG 创建→规则更新→删除；[日志](resource-live-sg-07.log)及[清理](live-sg-07/cleanup.json)。未模拟 KCN，未改共享服务。 |

真实 KCN 的 [创建](live-sg-07/create-ready.json)与[更新](live-sg-07/update-ready.json)均确认 Valid/Ready、observedGeneration 追平，原 UID 保持，generation 1→2；仅默认 ingress all/drop priority 1，egressRules 为空，组内互信关闭。删除通过产品 UC 和正常 finalizer 完成，随后任务 namespace 消失。

## 修复和未验范围

首轮 Network 编译的私有 helper 引用错误已修；PG 检查发现 Provider 名下划线及 nil KeepIds 不释放引用，已修复并通过真实 PG。容器纵向测试先后暴露缺可信 caller、缺 Image Runtime 与受限角色的 fixture 装配问题，保留生产身份/镜像 guard 后通过；热更同步管理版本，防止下一轮调谐触发额外生命周期变更。失败原日志保留在任务目录，不将中断运行计为 PASS。

Console 代码已落在 Fedora 原 `feat/governance-api@ad70a4f2` dirty 工作区，原 VM/存储改动保留；功能补丁 SHA256 `ce4ceb487418fb280748d910e21cf034ab4ebffea5ec5d2b04655ea34c1f3a20`。lint/格式/typecheck PASS；GitNexus 检测有明显索引错链并返回 critical，未记 PASS。未生产构建、未操作 dev。

共享 Resource/Gov 部署、权限目录/角色授权与 RBAC 应用：NOT_RUN。真实 EIP/VNic 成员规则与包过滤、真实容器/VM 全业务创建及浏览器验收：NOT_RUN。真实 SG 生命周期与受控 EIP/容器链不能替代这些验收。

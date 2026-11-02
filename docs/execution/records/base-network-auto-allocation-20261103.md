# 基础网络自动分配与 LB 产品主链（2026-11-03）

当前状态只见[执行状态](../status.md#base-network-auto-allocation-20261103)。业务合同见[基础连接规格](../../specs/vpc-connectivity-lb.md)与[平台运行前提](../../../deployments/egress/intranet.md)。

## 实施与固定输入

Resource 延续 PR #46 的 `cba0bcb`；Console 延续 PR #10 的 `1e032e4`，新增只读摘要提交 `c0765b6`；BOSS 从实际最新 main `9f917a4` 创建独立分支，提交 `221b4cd`；Governance 从 main `316ee2e` 创建配套分支。原有 dirty 主工作区及用户 43927 预览保留。

Resource 新增 `GetIntranetPoolDefaults` 和 `SetIntranetPoolAutoAllocationEnabled`；平台身份来自既有 mTLS 委托，池版本与永久幂等复用既有接受记录。自动开启在一个事务设置默认池、池开放及新 VPC rollout；关闭只允许当前默认源并保留池开放和现有资源。默认源切换在开启时校验并开放目标池，不移动已受理子资源。Intranet 池新增只读 `new_vpcs_enabled`，只有当前启用的默认源为 true。

迁移 0027 删除人工 Intranet verification 分配约束，并在既有平台操作类型 CHECK 中加入新命令；不改已应用迁移。准入与运行 adapter 均改为依赖真实 Provider 当前事实，不再以人工证据作为 Intranet EIP/SNAT 前置。创建表单默认身份及路由来自只读 Provider 查询，实际创建仍核对冻结的 UID、控制器默认 router、路由覆盖与 Ready。

## Fedora 检查

独占目录 `/home/chabking/tasks/base-network-20261103/resource`。初始输入 8542 文件，归档 SHA-256 `fb173cf7d799aae5db656efe0ef70bcc855c4f2ab08999c201b84ea1bb328a24`；后续精确差异保留于本 Git 提交及远端源码。Go 1.26.10，Buf 1.60.0，sqlc 1.31.1；GOMAXPROCS=2、GOFLAGS=-p=2。生成、格式化、数据库、全部测试与构建均在 Fedora 执行。

- `make config`：PASS；生成后网络 API 与正式输入同步。
- `go test -count=1 ./internal/biz/network ./internal/service/network ./internal/server`：PASS，包含所有平台方法的受信身份检查。
- `scripts/integration ./internal/data/network -run '^Test(Intranet|BaseConnectivity|BaseConcurrentWorkers)' -v -timeout=15m`：PASS，真实独占 PostgreSQL，运行 116.650 秒；覆盖原子默认/分配/开关、无人工 verification、永久重放/冲突/旧版本、切换旧源误关闭拒绝、真实默认身份/路由拒绝，以及新建 VPC→基础 EIP/SNAT→并发/未知结果恢复/删除。
- 最终 `make verify`：PASS，包括锁定生成一致性、分层故障检查、依赖 tidy diff、全 Go 单元检查、vet、build、模块校验与 diff 检查。

原始失败未覆盖：首次新增开关 PG 失败定位为平台操作类型 CHECK 未登记，已在 0027 修复；真实 VPC fixture 去除人工证据后发现 adapter 残余 verification gate，已移除并由同一真实生命周期复验通过。最初扩大至旧 backfill 测试时，它们在旧 VPC fixture 创建阶段因 `tenant namespace reader unavailable` 失败；该历史补齐路径不在本次实施范围，不修改断言或记为通过。具体日志为远端 `evidence/{postgres-tests-first-failed,postgres-tests-with-legacy-fixture-failures,postgres-tests-adapter-gate-failed,postgres-tests,make-verify}.log`。

## 发布与业务验收

本节待实际发布后追加回执，不以源码或 API 接受替代业务验收。目标为 `kubernetes-admin@ani-lab`，API `https://172.16.101.10:6443`，kube-system UID `5277649d-e28d-4a0a-ae76-8354365c63bc`。ani-03 原有 NotReady；两个 Ready 节点释放旧 Inference 规格样本后已回收 8 CPU/16Gi，大型 LB 仍需进一步容量或节点恢复。保留此前私网/公网 KCN 对照试验及用户 VM，不把这些手工 CR 视为本次产品管理资源。

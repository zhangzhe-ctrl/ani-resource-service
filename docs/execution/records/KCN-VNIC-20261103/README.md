# 手动 VNIC 实施证据

实现输入为[计划](../../../plans/kcn-vnic-management-20261103.md)，三仓固定基线分别为 Resource `46a2b8e`、Gov `b6262869`、Console `87b75acd`。当前状态只在[执行状态](../../status.md#kcn-vnic-management-20261103)维护。

## 源码与定向验证

- 主机：`ssh fedora`；Resource 独立源快照 `/home/chabking/tasks/vnic-resource-20261103/source`；Network PG 快照 `/home/chabking/tasks/vnic-network-20261103/source`。本地仅源码、Git 和传输。
- 工具：Go `1.26.10`、Buf `1.60.0`、sqlc `1.31.1`，生成物从正式 Proto/SQL 输入生成并回传。未手改生成物。
- `go test`：Network/VM/Instance 的领域、API codec、data、service、server 和组合根通过；新行为包括保存不修改运行端点、取消预留、模板版本与运行确认分离。
- 真实 PG：`TestManualVNicPGReplayReservationRetentionAndDelete`、`TestManualVNicPGExclusiveReservationAndStableRead`、`TestManualVNicPGServiceTrustedCaller` 全部通过。仅 KCN 分配回执使用外部边界替身；持久化、幂等、租户、预留、worker 与删除为真实业务代码。过程中修复删除观察未带分配事实时将地址写 NULL 的缺陷。任务 PG 容器已清理。
- 按仓库要求执行 `make verify`：生成一致性、领域边界、故障构建、mod tidy 及业务测试通过；旧 `scripts/lb-api` socket 用例因长 TMPDIR 失败。使用短临时目录单独复验该包通过，不重跑已通过测试。后续 `go vet` 指出新测试按值复制 protobuf 锁，改用 `proto.Clone` 后 `go vet ./...`、`go build -trimpath ./...`、`go mod verify`、`git diff --check` 通过。保留原失败，不将首次命令记为全绿。
- 原始日志：Fedora `/home/chabking/tasks/vnic-resource-20261103/` 对应本地采集的 `vnic-fourth-compile.log`、`vnic-verify.log`、`vnic-verify-remaining.log`、`vnic-verify-final-steps.log`；Network PG 日志在独立 Network run。Console `935321e` 的 Fedora 源码检查已通过并 SSH 推送。

## 真实环境

Fedora 经受控 SSH 登录 `ubuntu@172.16.101.10`，kube-system UID `5277649d-e28d-4a0a-ae76-8354365c63bc`；节点 10/11 Ready、12 NotReady。此前 BatchMode 密钥认证失败不是节点不可访问；旧 loopback kubeconfig 隧道不作为本轮集群凭据。

发布、页面、VM/容器数据面、SG 策略与显式释放尚待执行，不能由受控边界测试或 API 受理替代。未修改 KCN，未新建数据库备份或额外发布门禁。

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

## 页面到运行事实与待处理边界

2026-11-03 正式 Console 分支的独占预览完成 VM `22207203-060b-4f86-8666-186574150b04` 与 Notebook `fb41b4ac-d185-41b8-8aca-01f8cceafab8` 的默认自动创建。实际 Notebook→VM guest SSH，以及 guest→Notebook HTTP 均通过；guest 观察 `10.75.0.9 / fa:00:97:6b:b3:f5`，Notebook 为 `10.75.0.10`，另验证 Notebook→guest HTTP。未为默认模式新增手动产品记录。

页面申请 A `vnic_9609943037864545aa447e0ba8e02056`（10.75.0.11）及 B `vnic_f090bf4ede0347969c5598096f4288c9`（10.75.0.12）；A 原 UID `b4922de6-a321-492c-85af-944adb05f4db`。原创建请求同键重放保持产品 ID、operation ID、原 UID 和唯一 CR；重放仍返回原创建快照，地址以随后 GET 为准。请求与实际流量证据位于 Fedora task private 的 `create-replay-proof.json`、`default-network-proof.json`，页面原请求由 Console task 保存，不含 Authorization。

VM1 的保存 operation `8d2bc3dd-dc2c-4c2a-a14e-e8ad22b28ca1` 仍为 running（network provider temporary）。目标 VNIC spec 已引用 allow SG；SG Ready/observedGeneration=1，appliedVNics 同时包含目标 A 和当前自动 VNIC，但两张 VNIC 的 status 均无 appliedSecurityGroups。固定 KCN `6132623` 的 `vnic_controller.go:770` securityGroupUpdateHandler 为空；CRD 声明了该回执字段。目标 A 的 nic/dstNic/nodeName 均空，当前 Nic Attached 的仍是原自动 VNIC。用户已交 KCN 团队排查并要求保留相关资源；Res 不绕回执、不改 KCN、不删除该现场。

本批定向修正接正式纯读 Attachment 事实显示当前网卡/地址和产品名称；配置保存未完成时独立显示 configuring/operation reason，不能提前宣告待重启就绪。重核同一配置复用绑定，避免 Restart 的新 operation ID 生成重复有效关联。上述字段和读取/复用行为按其针对性用例验证，不重跑无关全量。SG 写入版本围栏单独暂存为后续批次，现场排查期间不部署该写路径。

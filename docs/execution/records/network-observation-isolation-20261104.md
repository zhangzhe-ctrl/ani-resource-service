# 网络观测故障隔离与终态恢复（2026-11-04）

基线为本次实际拉取的 main `d23828c95a634bab4cd979eb14d896e84830f283`，分支 `codex/network-observation-isolation-20261104`。修复范围仅 Resource；无迁移、配置修改或线上部署。

## 业务结果

真实 PG/worker 回归覆盖：正常创建 VPC/EIP/SNAT/Subnet → 暂停共享观察 → EIP 读取失败、VPC 聚合 degraded 且自身证据新鲜 → VNIC 创建仍按当前原 VPC/Subnet 受理，且不增加全量 LIST。VM 预检/Prepare、已有手动 VNIC 预留也在相同聚合异常下受理。UID 替换、删除、未决写入、配置未就绪继续拒绝；定向核验不刷新投影。

Released 协议阻塞记录消费晚于持久异常的共享证据，原封闭身份、revision、摘要、finalization 和实际无残留均满足才恢复；初次释放、迟到孤儿和活动删除的独立证明保留。Deleted LB 复用有效完整审计。SNAT 失败读取/门控保留已确认 applied 事实，基础父子证据恢复只在新鲜度跃迁时唤醒。可选 LB 读取和来源断点不再丢弃/失效完整基础证据，不完整 LB 索引不能证明清理完成。

## 验证

重任务均在 Fedora 独占目录 `/home/chabking/tasks/network-observation-fix-20261104/candidate`，沿用 `net05a-heavy.lock`，Go 1.26.10、sqlc v1.31.1、Buf 1.60.0。源文件清单和 SHA-256 校验后传输；sqlc 生成物和 gofmt 结果回传前检查本地文件未漂移。

| 检查 | 结果 |
|---|---|
| main + 新增回归的基线运行 | FAIL：VNIC 创建/预留被聚合阻塞，SNAT 事实被清空，原 VPC 就绪证据被丢弃，子项恢复未通知父级，可选 LB 超时丢弃基础证据 |
| `make sql` | PASS，远程固定 sqlc 生成，无 migration |
| `make verify` | PASS，exit 0，448.48s；生成一致性、分层、故障构建、tidy、全仓单测、vet、构建、模块校验均执行 |
| 相关 PG/领域回归 | PASS，36 个顶层测试、75 条含子项 PASS；network 包业务运行 47.04s，完整命令 141.54s |
| 对应 race 回归 | PASS，同组 36 个顶层测试，exit 0，193.86s，无 DATA RACE；临时容器已移除 |
| 10–12 集群部署/真实 VM 与 VNIC 验收、长期请求预算验证 | NOT_RUN |

定向 PG 和 race 复用同一组选择器，涵盖新用例及既有 Namespace/UID、墓碑、迟到依赖、owner 关闭后采集、VM/VMI 证明和手动 VNIC 留存/删除用例。核心可复现命令：

```bash
scripts/integration ./internal/data/network ./internal/biz/network \
  -run '^(TestVNic(AdmissionPG|ReservationPG)|TestVMNetworkPreflightPG|TestBaseObservation|TestOptionalLB|TestReleasedProtocol|TestDeletedLoadBalancerReuses)' \
  -timeout 240s -v
```

完整命令与原始回执在 Fedora 任务目录的 `baseline-result.json`、`generate-result.json`、`verify-result.json`、`targeted-result.json`、`race-result.json` 和对应日志。基线/中间失败原样保留；测试夹具的 consumer_type、pending_since 和 VMPrimary 按当前合同补齐，未删除负向断言。`make verify` 有非致命 shell 初始化和旧模块元数据缓存权限警告，最终退出码为 0；没有更改全局环境掩盖它们。

`make verify` 完成于最后两处回归夹具修正及纵向用例补充前，所有生产源码及生成物与最终验证输入相同；随后修改的测试由最终 PG 和 race 命令重新编译、执行。未因测试记录/文档补充重复全仓构建。

数据库使用 `scripts/integration` 的独占临时 PG 容器和逐测试数据库，仅 KCN 外部 HTTP 边界受控。已完成命令的清理回执记录临时容器移除。受控回归不等于现场 KCN 连通、guest 启动或线上预算已验证，也不认定此前 API Server/etcd 读超时已解决。

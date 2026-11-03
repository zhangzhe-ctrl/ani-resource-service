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

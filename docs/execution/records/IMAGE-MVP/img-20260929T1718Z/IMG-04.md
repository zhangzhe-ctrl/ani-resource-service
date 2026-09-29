# IMG-04 · 空间与凭证的持久化恢复

最终代码 SHA `53f6c1640792cb5fcd53c541ba57943c2b6f7fc3`，已推 review 分支。[Fedora 全部门禁日志](IMG-04/53f6c1640792cb5fcd53c541ba57943c2b6f7fc3-verify/output.txt) exit 0，2026-09-29T18:32:00Z—18:34:25Z：新 SHA 再生成无差异、定向 race、真实 PG + race、两项租户 mutation、完整 make verify。此前 `1a7e312` 通过后新增重置边界覆盖，因此以本 SHA 为最终证据。CPU/内存/重锁/TMPDIR 见 runner；原 Network 保护路径 diff 仍为0。

| Task | 实现和证据 |
|---|---|
| IMG-04.1 | `Lifecycle.EnsureImageSpace/GetImageSpace/resumeProject`；同slug同/新key复用，Project和pull提交后 available；`TestLifecycleEnableIssueResetDisableReplay`，已有并发slug真PG反例继续通过 |
| IMG-04.2 | `WithSpaceWriteLock` + 数据库未完成外部command唯一性；`TestLifecycleTwoProcessesSameCommands` 两个真实进程同时 enable/issue，1 Project、2 Robot、2 set-secret；PG锁超时和释放测试继续通过；HTTP不在长事务中 |
| IMG-04.3 | `prepareCandidate/runCandidate/ActivateTenantCandidate`；先保存稳定密文，再创建/设置/确认Robot；pull长期密文使用无command的独立AAD重加密，Enable完成时清除临时交付密文；真实PG解密对照通过 |
| IMG-04.4 | `IssuePublisherCredential/ResetPublisherCredential/replayDelivery`；新代次、旧身份停用、版本CAS、actor/key/fingerprint检查、10分钟和当前代次限制；`TestStaleGenerationCannotActivateOrReplay` 旧command无法激活/交付/改写当前Secret |
| IMG-04.5 | `GetPublisherCredential/DisablePublisherCredential`；not_issued元数据、无Secret GET、停用重放、publisher表无长期Secret；同用例回归通过 |
| IMG-04.6 | `TestLifecycleResumeAtPersistenceAndProviderBoundaries` 8种失去响应/提交回执；`TestLifecycleActualProcessExitRecovery` 13个实际退出73→新进程恢复0场景，覆盖Project ID落盘、候选密文、Robot创建、secret设置、旧Robot停用与新代次事务完成；使用真实PG和固定TLS协议fixture，未冒称真Harbor |
| IMG-04.7 | `RecoverProjectBinding/InspectSpace/PurgeExpiredDeliverySecrets`；未知同名与Project响应丢失保持ownership blocked，平台caller+明确ID+核验hash才可恢复，证据hash保留；租户调用拒绝；过期清理仅命中2个过期交付command。CLI封装仍由IMG-07实现 |

PG 18.6 固定digest，每个容器768MiB、无额外swap、1 CPU。最终容器 `b8fe49e5dd07253f375e68faa084458132925404e4e67ffeb8dd535556cd7adc`、`4c22159852dc6fa7078fa70718d3e9aa6084cde8c4450f227cf72f3afb72cac1` 均核对ownership后删除；前轮容器也按日志清理。受控子进程PID/exit全部记录，没有任务外kill。未接入共享Harbor/集群；live仍缺批准profile，product此批n_a。

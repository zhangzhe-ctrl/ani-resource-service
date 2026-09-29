# IMG-04 · 空间、凭证与失败恢复

目标：实现自动化所必需的有限副作用恢复，不引入后台worker或统一作业服务。

依赖：IMG-02, IMG-03。预计 2～2.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-04.1 | 空间幂等入口 | Spaces.EnsureImageSpace/GetImageSpace/resumeEnable | 同租户同slug重复成功、不同slug冲突；并发slug只有一个owner；Project/Pull均ready才available |
| IMG-04.2 | 空间级有限串行化 | WithSpaceWriteLock；command唯一外部写约束 | 两个进程同时推进同command不重复签发；HTTP不在长PG事务；连接断开/超时不复用污染连接 |
| IMG-04.3 | 运行凭证初始化 | ensurePullCredential/prepareCandidate/applyCandidate/activateCandidate | 只读两Project；cipher保存成功后才ready；运行Secret不出现在Enable响应 |
| IMG-04.4 | 发布签发与重置 | IssuePublisherCredential/ResetPublisherCredential/replayDelivery | 新Robot代次、旧代次停用、版本CAS；同键同actor10分钟重放；不同actor/旧代次/过期不得拿Secret |
| IMG-04.5 | 元数据与停用 | GetPublisherCredential/DisablePublisherCredential | GET不含Secret；未签发返回not_issued；停用幂等；不声称立刻撤销已发Token |
| IMG-04.6 | 故障注入恢复 | TestResumeAfterProjectBind/TestResumeAfterRobotCreate/TestResumeAfterSecretSet/TestStaleGeneration | 在每一持久化/Harbor边界注入超时和进程退出，恢复同key；未知同名Project明确blocked而非错误认领 |
| IMG-04.7 | 受控恢复与过期清理 | Platform.RecoverProjectBinding/InspectSpace；PurgeExpiredDeliverySecrets | 只能operator+明确ID+归属核验；正常租户不需要管理员逐个操作；清理不删除其他command或镜像 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

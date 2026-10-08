# IMG-00 · 基线、接入点与 Fedora 执行边界

目标：先证明读到的代码、运行约束和待接入资源是真实的；允许记录局部阻塞，不把未知项填成已部署。

依赖：无；先读取任务包。预计 0.5～0.75 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-00.1 | 读取并冻结源码 | Resource/Governance AGENTS、START-HERE、ADR2/6、remote-execution、go.mod；本批不修改业务方法 | source-lock.json记录两仓完整SHA、干净状态、分支、工具版本；差异不能重置用户代码 |
| IMG-00.2 | 定位真实接入点 | Resource config Validate/main/governance；Governance HTTP注册、DI、permission/module seeds | bindings.json列每个真实文件和方法；新增目标与已存在方法分开 |
| IMG-00.3 | 绑定普通容器和前端 | 搜索已允许的非ANI仓库及当前工作区；找实际Create/PodTemplate/tenant namespace/前端route | 记录repo+SHA+路径+方法；找不到标明blocked:consumer或blocked:frontend，不造服务、不访问ANI |
| IMG-00.4 | 探测远端和限制 | ssh fedora；读取当前重锁/真实HOME；确认Go1.26.7、磁盘、内存、容器工具和已有PG runner | 所有探测重命令在Fedora；确认互斥重锁、隔离run root、资源配额和日志路径 |
| IMG-00.5 | 核对Harbor与授权 | 固定管理URL/registry authority/TLS、实际Harbor version/OpenAPI、management权限和安全策略 | 没有Harbor或写授权只阻塞真实探测；保留离线单测实施，不能擅自安装/升级或降安全策略 |
| IMG-00.6 | 导入计划与建立账本 | 只导入本包明确新增文档；向现有docs/execution/status.md追加Image区块 | 不覆盖README/AGENTS/历史状态；填写live profile明确cluster fingerprint与run-owned资源范围 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

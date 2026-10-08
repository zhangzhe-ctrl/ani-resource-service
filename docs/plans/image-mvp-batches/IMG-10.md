# IMG-10 · 联合验收、隔离反例与最终回归

目标：证明真实业务闭环和隔离，同时不破坏已有Network。

依赖：IMG-07, IMG-08, IMG-09。预计 1～1.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-10.1 | 冻结live输入 | source-lock、镜像与cluster指纹、两测试tenant/user、namespace、CA、run-owned allowlist | 没有live批准不得写共享集群；凭证走文件；记录实际API/节点而不是只截图 |
| IMG-10.2 | 正向产品链路 | ANI enable→issue→Docker push→register→真实container create | 业务API+Pod spec固定Digest+容器唯一输出标记；平台镜像两个租户可用 |
| IMG-10.3 | 越权与缓存测试 | 按image-acceptance矩阵执行A/B、发布/只读、共享节点缓存负例 | API与Registry两层拒绝；不可只测404页面；直接Pod入口的准入前置有证据 |
| IMG-10.4 | 重试/撤销/Tag变更 | 并发幂等、响应丢失、凭证停用、Tag重推、取消登记 | 旧业务意图不变；停用按Token生命周期测试；原登记取消不影响已创建任务 |
| IMG-10.5 | 全量门禁 | Fedora make verify/integration/race/tenant-mutations + image门禁；两仓对应测试 | 必须真实exit0；没有关闭旧门禁；Network迁移checksum、TLS/旧RPC回归一致 |
| IMG-10.6 | 收集和清理 | 脱敏manifest、命令及exitcode、日志、run-owned ID/UID；限域cleanup | 清理核验创建ID/UID和ownership；不跑全局GC、不删除共享project/namespace/volume；剩余项写报告 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

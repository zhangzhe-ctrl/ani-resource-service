# IMG-02 · Image schema、迁移与显式租户数据层

目标：建立无RLS、独立所有权的数据层，不侵入Network的历史迁移。

依赖：IMG-01。预计 1～1.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-02.1 | 独立迁移输入 | migrations/image/001_image.sql、embed.go；image.schema_version | 真PG可首建/重复校验；旧migrations/*.sql的bytes/checksum完全不变 |
| IMG-02.2 | 迁移入口和权限 | ApplyImageMigrations/CheckReady；-image-migrate互斥分支 | owner执行DDL、runtime拒绝DDL/SET owner；-migrate仍只Network；Image禁用不要求schema |
| IMG-02.3 | SQLC查询 | spaces/credentials/registrations/commands.sql；sqlc.yaml新entry | SQL有显式tenant与scope；生成数据包不 import Network sqlcgen |
| IMG-02.4 | 事务和状态写 | ReserveTenantSpace、ApplyTenantRegistration、ApplyTenantMetadata、CompleteCommand | 登记与command结果同事务；FK/CAS/唯一性失败正确映射，不吞错误后返回成功 |
| IMG-02.5 | 独立真DB测试runner | scripts/image-integration、make image-integration；角色/DSN文件和临时库 | 只在Fedora本run PG；禁止所有必测case因缺配置skip；不改共享DB |
| IMG-02.6 | 隔离变异与回归 | TestTenantCompositeFK/TestPlatformFK/TestRuntimeRole/TestCAS；image tenant mutation fixture | 故意去tenant谓词/FK在隔离副本导致测试失败；恢复hash；原make integration/tenant-mutations不被削弱 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

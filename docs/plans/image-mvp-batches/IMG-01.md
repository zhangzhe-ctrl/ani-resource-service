# IMG-01 · 契约、领域类型与纯规则

目标：先冻结协议及纯业务类型，避免后续每批重新猜字段。

依赖：IMG-00。预计 0.5～0.75 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-01.1 | 落入Resource Proto | api/image/v1/image.proto、runtime.proto；TenantImageService 11方法与Runtime 2方法 | Buf在Fedora校验；确认与实际buf module root的import路径匹配，不手写pb.go |
| IMG-01.2 | 落入公开DTO与HTTP协议 | Governance catalog/image.proto、admin/i_image.proto、buf.image.gen.yaml | 公开DTO无tenant/namespace/admin字段；内部pull材料不进入公共OpenAPI |
| IMG-01.3 | 领域对象与窄端口 | biz/image/types.go、ports.go；Space、CredentialInfo、Registration、Command、RuntimeImages | biz无grpc/kratos/http/pgx/k8s依赖；按现有package biz风格 |
| IMG-01.4 | 纯验证器 | ParseTenant/ParseSlug/ParseImageID/ParseImageReference/NormalizeMetadata | TestValidationMatrix覆盖零UUID、外部host、编码斜线、越级project、空tag、超长、非法枚举 |
| IMG-01.5 | Caller与错误 | WithCaller/RequireTenant/RequireRuntime/RequirePlatform；Fail/ReasonOf | TestCallerFailClosed证明无身份/错误身份拒绝，平台不存在empty-tenant旁路 |
| IMG-01.6 | 远端生成回传 | Fedora make generate/定向Gov生成；gofmt只在Fedora；回传生成差异 | 本地检查基线hash并正常commit/push；新SHA再远端生成无差异，契约报告明确尚无业务实现 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

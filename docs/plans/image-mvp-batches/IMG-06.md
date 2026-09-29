# IMG-06 · Resource 安全入口与 Governance 适配

目标：让ANI页面/API能真正使用镜像能力，同时保持两仓既有身份合同。

依赖：IMG-01, IMG-04, IMG-05。预计 1.5～2 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-06.1 | 薄gRPC service | TenantService 11 RPC；wireSpace/wireRegistration/wireCredential/rpcError | 仅协议转换，错误Image ErrorInfo与API文档一致，无业务SQL/Harbor调用 |
| IMG-06.2 | Resource入口接线 | Bootstrap.image=3、配置Validate、runGovernance、image_identity bridge | 精确白名单；可信metadata与请求tenant一致；full/vpc-read入口不暴露Image；保留legacy SAN |
| IMG-06.3 | Governance下游client | NewImageClient/ImageConfigFromEnv/trustedImageCall、11方法 | mTLS复用已定服务器身份但不改Network配置；NewOutgoingContext重建；依赖固定已推送Resource SHA |
| IMG-06.4 | BFF与响应校验 | NewImageService/trustedImageOperator/validateImageReply/mapImageError | 复用ResourceTenantResolver；uint32→UUID；返回归属不符failclosed，不转发内部tenant/Secret |
| IMG-06.5 | 模块/权限/脱敏 | IMAGE=14实际冲突检查；image_policy、注册/DI、定向生成/OpenAPI | 默认只读与发布管理员区分；凭证body日志排除、no-store；公共身份头不能覆盖认证上下文 |
| IMG-06.6 | 真实HTTP与mTLS反例 | image_joint_http_test、image_identity_test | JWT/AK允许路径、无权限、错误SAN、重复tenant头、body伪造、错误下游响应；Secret接口不在公共schema |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。

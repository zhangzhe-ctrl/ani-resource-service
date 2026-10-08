# IMG-03 · Harbor 协议与密文适配

代码 SHA `c17d64f0895925d623a4d7f70ca57b5f0912b3b0` 已推 review 分支。[Fedora 定向 race 和完整 verify](IMG-03/c17d64f0895925d623a4d7f70ca57b5f0912b3b0-verify/output.txt) exit 0；2026-09-29T18:09:39Z—18:11:11Z。新 SHA 重新生成无差异。生成输入、回传白名单/base hash、runner、日志及 SHA256SUMS 均在本记录目录。

| Task | 实现 / 定向断言 |
|---|---|
| IMG-03.1 | `NewHarbor/doJSON/mapHarborError`；`TestHarborTransportBoundary` 实际 TLS 服务器验证错误 CA、超时、重定向不发送凭证、4MiB 响应上限、400/401/403/404/409/429/5xx、GET 最多2次/写请求1次、Secret 错误体脱敏 |
| IMG-03.2 | `GetProjectByID/FindProjectByName/CreatePrivateProject`；`TestHarborPrivateProjectAndLocation` 验证字符串 metadata.public、返回 ID、丢失/非法/跨源 Location 不认领 |
| IMG-03.3 | `FindOwnedRobot/CreateRobot/GetRobot/SetRobotSecret/SetRobotDisabled`；`TestHarborRobotOwnershipAndWireContract` 验证实际 PATCH path、空 Secret 回显、PUT 保留身份/期限、错误 ownership 不发写请求 |
| IMG-03.4 | `RobotPermissions/ValidateRobotPermissions`；`TestRestrictedRobotPermissions` 验证三类限定模板、跨项目/删除/system 权限拒绝、push 必须同时 pull；用户名来自 provider，查找前缀显式部署配置 |
| IMG-03.5 | `ResolveArtifact/GetArtifactByDigest/readRunnablePlatforms`；`TestHarborArtifactDigestAndPlatforms` 验证 tag→digest 二次读取、manifest/index、明确 attestation 忽略、chart/非容器/未知平台/平台冲突/描述符上限/环/深度拒绝，不访问 layer 或外部链接 |
| IMG-03.6 | `NewAESGCMKeyring/Seal/Open`；`TestCipherWrongAADTamperKeyMissing` 验证随机 nonce、7项 AAD 变更、篡改/缺失或错误 key/截断拒绝、typed secret 格式与 JSON 不泄明文 |

上游固定依据见 [source lock](harbor-source-lock.md)。这是 httptest/加密/边界隔离通过，不是实版 Harbor/live 通过。live blocked：缺批准 profile、实际版本/CA/权限绑定。无共享 Harbor 或集群副作用；本批没有启动 Docker 资源。内部 Registry mutation 端口接收完整预期 Robot 以便 adapter 写前校验身份，避免裸 ID 写操作；无公开 API 变化。

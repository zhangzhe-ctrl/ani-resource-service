# Image 部署接线

本目录是 Image MVP 的配置说明，不是部署或 live 验收记录。当前结果只看
[执行状态](../../docs/execution/status.md#image-mvp)；真实写入先满足
[Fedora 手册的 live profile](../../docs/runbooks/image-mvp-fedora.md)。
现有 Network 配置、数据库、证书身份及迁移入口保持各自职责。

## Resource 服务

将 [image.example.yaml](image.example.yaml) 的 `image` 段追加到部署方已有的完整、
有效配置中。示例默认关闭；启用前替换所有占位符，使用运行身份可读的绝对路径。
`ANI_NETWORK_MODE=governance` 才注册 Image 租户 RPC；full/vpc-read 模式不注册。
Governance 模式仍装配现有 Network worker，并需要原 Network 配置及 Provider 权限，
不能把仅有 `image` 段的文件当成完整服务配置。

Resource 入站继续使用 `ANI_NETWORK_CLIENT_CA`、`ANI_NETWORK_TLS_CERT`、
`ANI_NETWORK_TLS_KEY`。服务端证书的 DNS SAN 包含既有 `ani-network-service`，
受信 Governance 客户端证书包含 `ani-governance`；TLS 最低1.3。
这些服务间证书与 Harbor HTTPS CA 是不同的配置用途。
Image 仅增加固定11项租户操作；平台管理和运行拉取材料没有公共浏览器入口。

镜像外部调用有界；配置 `server.grpc.timeout` 时需给空间/凭证写入留下足够时间，
例如45秒，Governance Image 客户端超时默认30秒、最多45秒。
超时后保留原幂等键与请求内容重试，不将超时视为未发生副作用。
Harbor 暂不可用不导致 Network 因 Image 启动探测而退出；Image 配置或数据库检查失败
会拒绝启动。启动时数据库检查通过也不代表持续 Harbor/数据面可用。

## 文件与数据库

- 数据库 DSN、Harbor 管理密码、AES-GCM keyring、游标签名密钥均通过独立私有文件注入。
  文件须为普通文件、非符号链接、其他用户不可读写，建议目录0700、文件0600；
  每文件最多64 KiB。不得把内容放到命令行、源码、ConfigMap或公开日志。
- Kubernetes Secret volume 的投射文件通常是符号链接，不能直接传给私有文件读取器。
  部署方须在启动前将所需文件复制到该 Pod 独占的受限目录，设置服务 UID 和0600，
  再使用普通文件路径；保留原 Secret 管理与轮换责任，不修改读取器来忽略检查。
- `harbor_ca_file` 是验证 Harbor 服务端的 PEM CA。`harbor_url` 必须为 HTTPS，
  当前同时提供管理 API 与镜像 registry authority；不接受任意 URL path 或关闭证书校验。
  `robot_name_prefix` 必须与实际 Harbor 配置一致，不根据测试前缀猜测生产配置。
- keyring JSON 的形状为 `{"active_key_id":"<id>","keys":{"<id>":"<base64 AES key>"}}`。
  保留解密已有密文所需的旧 key；游标文件为至少32字节随机密钥的严格 base64。
  安装 ID、registry authority 和空间绑定不可通过更换配置重认领。
- Image 使用单独 runtime DSN；运行角色只有 `image` 业务表 SELECT/INSERT/UPDATE、
  `image.schema_version` SELECT，无 owner/超级用户/BYPASSRLS/建库/建角色/跨域表权限。
  不复用 Network runtime DSN。表关系与约束见[数据契约](../../docs/specs/image-data.md)。

迁移使用同一二进制的 `-image-migrate`，通过 `ANI_IMAGE_MIGRATION_DSN_FILE`
和 `ANI_IMAGE_RUNTIME_ROLE` 明确 owner DSN 私有文件及待授权运行角色。
owner 只用于该独立迁移入口；原 `-migrate` 继续只处理 Network。
执行迁移前按实际数据库备份/恢复流程冻结输入；本说明不授权操作共享数据库。

## Governance 接线

设置 `ANI_IMAGE_ADDR`、`ANI_IMAGE_CA`、`ANI_IMAGE_CERT`、`ANI_IMAGE_KEY`，
可选 `ANI_IMAGE_TIMEOUT`。目标是上述受信 Resource listener；配置缺失不建立
明文或默认身份连接。客户端只使用固定版本的生成契约，复用既有租户 UUID 映射。

部署 Governance 后先按其已有 OpenAPI catalog 同步流程同步 Image 路由，再由管理员
按 Governance `sql/data/README.md` 执行 Image 权限 catalog seed。
catalog 不自动给角色、API Key 或套餐授权。按实际角色显式配置只读四项权限与
需要的发布权限，并给相应套餐启用 IMAGE 模块；浏览器按钮状态不替代服务端权限检查。
不重置现有账号密码或绕过现有 Cookie/JWT/AK 身份流程。

## 验证与回退边界

先验证关闭 Image 时原 Network 行为，再在隔离数据库上验证 Image 配置、schema、
受信证书和精确方法边界。真实 Harbor 的版本、CA、Project范围、测试租户、允许清理
对象须来自已批准 profile；看到可访问的 Harbor 或 Kubernetes 不构成写入授权。

关闭 Image 可停止新的 Image API 入站，不会删除 Harbor Project/Robot、登记数据或
调用方拥有的 Namespace/Secret。已创建工作负载保留其固定 Digest 意图；凭证及对象
清理由相应所有者按实际 ID/UID 和本 run 的 ownership 处理。没有自动全局清理或迁移回滚。

## 本机受控平台命令

同一二进制使用 `-image-admin=<action>`，不启动服务 listener/Network worker。
必须给 `-image-operator-config` 一个0600普通文件，其中固定 `subject`、`actor`、
`installation_id`、`secret_output_directory`。安装 ID 必须等于 `-conf` 中启用的
Image 安装 ID；平台 actor 不冒充 Governance 用户。
`secret_output_directory` 必须为绝对路径、非符号链接、其他用户不可访问的目录。
operator 文件只交给已授权的后台运维执行身份，不给浏览器或工作负载。

请求为严格 JSON，默认从 stdin 读取，或给 `-image-admin-input` 一个0600普通文件；
未知字段、重复顶层字段、额外 JSON、任意 tenant/scope/SQL/URL 字段均拒绝。
以下是各动作输入字段；所有幂等键8～128字符，同一次重试必须保持 actor/key/body。

| action | 输入 |
|---|---|
| `init-platform` | `idempotency_key`；Project 名只来自配置 |
| `issue-platform-publisher` | `idempotency_key`、`expected_version`、`rotate`（首次false；显式轮换true） |
| `register-platform` | `idempotency_key`、`image_reference`、`display_name`、`description`、`purposes`、`accelerator` |
| `update-platform` | `idempotency_key`、`image_id`、`expected_version`及上行四项可变元数据；没有镜像引用字段 |
| `unregister-platform` | `idempotency_key`、`image_id`、`expected_version` |
| `inspect-space` | `space_id`；只输出空间事实，不输出 Robot Secret |
| `recover-project` | `space_id`、`project_id`、`evidence_sha256`；先确认实际私有 Project ID/名称及所有权证据 |
| `purge-expired-secrets` | `{}`；只清除已成功且交付窗口到期的 command 密文 |

签发必须额外指定 `-image-secret-output`，其绝对路径必须位于上述输出目录且文件名
仅含字母数字、点、短横线或下划线，以字母数字开头。命令在外部调用前以0600和
exclusive-create 保留新文件，存在的文件或符号链接一律失败。普通 stdout 只含
动作、操作者、元数据；Secret 仅写到此文件的 `secret` 字段，用户名在
`credential.Username`。首次签发 `expected_version=0`；后续使用返回的实际
`CredentialInfo.Version`，不要把 generation 当 version。

例如，在批准的 Fedora run 中，对已准备的私有 operator/runtime 配置执行：

```bash
"$IMAGE_BINARY" -conf "$IMAGE_RUNTIME_CONFIG" \
  -image-operator-config "$IMAGE_OPERATOR_CONFIG" \
  -image-admin=init-platform -image-admin-input "$IMAGE_INIT_REQUEST"
"$IMAGE_BINARY" -conf "$IMAGE_RUNTIME_CONFIG" \
  -image-operator-config "$IMAGE_OPERATOR_CONFIG" \
  -image-admin=issue-platform-publisher -image-admin-input "$IMAGE_ISSUE_REQUEST" \
  -image-secret-output "$IMAGE_NEW_CREDENTIAL_FILE"
```

`init-platform` 不认领未知同名 Project；没有已落库绑定时会阻塞，必须经明确的
`recover-project` 和外部所有权证据恢复后再重试原请求。新建空间不签发发布身份。
同 key 的 Secret 交付重放最多10分钟，且当前有效代次/version必须仍匹配；已轮换或
已过期交付不会返回旧 Secret。文件写入失败时保留原请求并使用一个新文件名重放，
不要不检查现状就改 key。只有显式 `rotate=true` 才替换已有发布身份并停用旧 Robot。
取消登记不删除 Harbor 内容；本命令没有任意对象删除或 HTTP 透传能力。

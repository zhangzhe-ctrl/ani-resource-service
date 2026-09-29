# 文件与方法级实施清单

以下 Image 文件/方法全部是新增目标。已存在的 Network/Governance 文件仅在“现有接入点”列明；未知前端/创建方路径由IMG-00绑定，不能自行伪造。保持当前 `package biz/data/service` 风格，composition root 对同名包使用 imagebiz/imagedata/imageservice 别名。

## 1. Resource 目录

```text
api/image/v1/image.proto                       TenantImageService + DTO
api/image/v1/runtime.proto                     仅内部运行契约
internal/biz/image/{types,errors,caller,validation,cursor}.go
internal/biz/image/{spaces,credentials,catalog,runtime,platform}.go
internal/biz/image/ports.go
internal/data/image/{postgres,migrate,spaces,credentials,catalog,commands}.go
internal/data/image/{harbor,harbor_project,harbor_robot,harbor_artifact,secret_cipher}.go
internal/data/image/queries/*.sql
internal/data/image/sqlcgen/*                  生成物
internal/service/image/{tenant,runtime,wire,errors}.go
internal/server/image_identity.go             Image专用上下文桥接/内部身份校验
cmd/ani-resource-service/{image,image_admin,image_migrate}.go
migrations/image/{001_image.sql,embed.go}
```

测试与被测包就近放置 *_test.go；真实PG测试单列 *_integration_test.go、独立runner显式提供数据库与角色，不允许未配置时整批skip却记PASS。

## 2. biz/image：纯业务用例

| 文件/类型 | 方法签名（领域类型在types.go定义） | 责任 |
|---|---|---|
| caller.go | WithCaller(ctx,Caller) context.Context；CallerFromContext(ctx)(Caller,error)；RequireTenant(ctx,tenant)；RequireRuntime(ctx,tenant)；RequirePlatform(ctx) | 不解析HTTP/metadata，消费server注入的可信身份；无默认允许 |
| validation.go | ParseTenant(raw)(string,error)；ParseSlug(raw)(string,error)；ParseImageID(raw)(string,error)；ParseImageReference(authority,project,raw)(ImageReference,error)；NormalizeMetadata(Metadata)(Metadata,error) | UUID非空规范形式；固定host/project；用途和长度限制 |
| errors.go | Fail(Reason,message) error；ReasonOf(error) Reason | Image自有reason；message脱敏 |
| cursor.go | NewCursorCodec(key)；EncodeCursor(TenantScope,Filter,PageKey)(string,error)；DecodeCursor(...)(PageKey,error) | HMAC cursor，不依赖protobuf/sqlc |
| spaces.go / Spaces | NewSpaces(repo,commands,provider,credentials,policy,clock)；EnsureImageSpace(ctx,EnableSpace)(Space,error)；GetImageSpace(ctx,tenant)(Space,error)；resumeEnable(ctx,Command)(Space,error) | 预留、已知绑定恢复、有限重试、完成状态；不写Namespace |
| credentials.go / Credentials | NewCredentials(repo,commands,provider,cipher,policy,clock)；GetPublisherCredential(ctx,tenant)(CredentialInfo,error)；IssuePublisherCredential(ctx,IssueCredential)(CredentialDelivery,error)；ResetPublisherCredential(ctx,ResetCredential)(CredentialDelivery,error)；DisablePublisherCredential(ctx,DisableCredential)(CredentialInfo,error) | 用户侧三种操作；幂等/版本/交付窗口 |
| credentials.go / Credentials | ensurePullCredential(ctx,Space,Command)(CredentialInfo,error)；prepareCandidate(ctx,Command)；applyCandidate(ctx,Command)；activateCandidate(ctx,Command)；replayDelivery(ctx,Command)(CredentialDelivery,error) | 共用小范围Robot恢复逻辑，不抽取成全平台工作流 |
| catalog.go / Catalog | NewCatalog(repo,commands,provider,cursor,clock)；RegisterImage(ctx,RegisterImage)(Registration,error)；GetImage(ctx,ReadImage)(Registration,error)；ListImages(ctx,ListImages)(RegistrationPage,error)；UpdateImage(ctx,UpdateImage)(Registration,error)；UnregisterImage(ctx,UnregisterImage)(Registration,error) | tenant权限+platform显式只读，Digest冻结，登记事务 |
| runtime.go / Runtime | NewRuntime(catalogRepo,credentialRepo,provider,cipher,clock)；ResolveImageForWorkload(ctx,ResolveImage)(ResolvedImage,error)；GetTenantPullMaterial(ctx,tenant)(PullMaterial,error) | 只对受信创建方；无K8s import；不发admin；不复用公开错误旁路 |
| platform.go / Platform | NewPlatform(...)；InitializePlatform(ctx,InitPlatform)(Space,error)；IssuePlatformPublisher(ctx,IssueCredential)(CredentialDelivery,error)；RegisterPlatformImage(ctx,RegisterImage)(Registration,error)；UpdatePlatformImage/UnregisterPlatformImage；InspectSpace；RecoverProjectBinding | 受控CLI复用核心验证；明确平台用例，不允许tenant caller调用 |

`ensurePullCredential`是空间初始化内部步骤，不对外新增一个“先预占再确认”的业务API。类型名与构造参数可在IMG-01做必要小范围整合，但不能改变已定API/权限/数据库合同。

## 3. ports 与 data/image

biz接口不引用net/http、pgx、Kubernetes、gRPC、Kratos。示例窄运行接口见 [Go契约](../plans/image-mvp-contracts/go/runtime_ports.go)。其他Repository接口按下面目标定义，由单一Image Postgres实现即可，不创建泛型通用仓储。

| adapter/文件 | 方法 | 必要实现细节 |
|---|---|---|
| Postgres/postgres.go | OpenPostgres(ctx,Config)(*Postgres,error)；Close()；CheckReady(ctx) error | Image连接池/角色/schema版本；不连接Network数据API |
| migrate.go | ApplyImageMigrations(ctx,OwnerConfig) error；VerifyImageMigrationChecksums(ctx) error | 独立锁、checksum、显式owner执行；默认启动不迁移 |
| spaces.go | ReserveTenantSpace；FindTenantSpace；FindPlatformSpace；SaveProjectBinding；MarkSpaceAvailable；MarkSpaceBlocked | tenant参数必需，平台独立方法，事务内写command关联 |
| credentials.go | GetTenantPublisher；GetTenantPull；ReserveCandidate；ActivateCredential；MarkCredentialDisabled | 当前有效代次切换；publisher不长期明文/可解密保存 |
| catalog.go | ApplyTenantRegistration；FindTenantRegistration；PageTenantRegistrations；ApplyTenantMetadata；ApplyTenantUnregister；对应Platform方法 | 每个Apply把结果与幂等完成同事务；版本CAS |
| commands.go | FindTenantCommand；ReserveTenantCommand；SaveCommandPhase；CompleteCommand；MarkRetryable；MarkBlocked；PurgeExpiredDeliverySecrets | typed结果编码，禁止raw provider body；临时Secret过期校验 |
| commands.go | WithSpaceWriteLock(ctx,spaceID,fn) error | 专用连接/session lock/超时/释放，断链丢弃连接；不保持长SQL事务 |
| Harbor/harbor.go | NewHarbor(Config)(*Harbor,error)；doJSON(ctx,method,path,in,out) error；mapHarborError(status,body) error | 固定TLS上游、CA、no跨host redirect、响应上限、头/体脱敏、限定重试 |
| harbor_project.go | GetProjectByID；FindProjectByName；CreatePrivateProject | 创建`metadata.public="false"`；保留现有安全策略；绑定ID后核验 |
| harbor_robot.go | FindOwnedRobot；CreateRobot；GetRobot；SetRobotSecret；SetRobotDisabled；ValidateRobotPermissions | description/name/ID全匹配；只对当前command候选或明确上一代操作 |
| harbor_artifact.go | ResolveArtifact(project,repository,reference)；GetArtifactByDigest；ReadRunnablePlatforms | 固定根digest，限制索引深度与大小；不下载layer，不接受任意host |
| secret_cipher.go | NewAESGCMKeyring；Seal(ctx,AAD,plain)(EncryptedSecret,error)；Open(ctx,AAD,encrypted)(Secret,error) | 标准库、key_id、随机nonce、AAD绑定；密文失败不重建 |

当前Root `scripts/verify-source` 已覆盖文件树；一般只需要使新生成入口加入sqlc/Buf配置，不重写它。`scripts/verify-boundaries`增补真实Image违规fixture，不能把biz对http/pgx的依赖加白名单。

## 4. service 与 server

`TenantService`逐个实现Proto的11个RPC，使用`NewTenantService(spaces,credentials,catalog)`；`RuntimeService`实现两个内部RPC。`wireSpace/wireCredential/wireRegistration/wireResolvedImage`只转换DTO；`rpcError`返回Image ErrorInfo，不能导入Network的service实现。

`internal/server/governance.go`追加Image方法组并在通过原有mTLS/单值metadata校验后调用 `withImageGovernanceCaller`；tenant_id必须规范、与可信metadata相同，拒绝body越界。`image_identity.go`只包含Image桥接，不改旧Network caller类型/鉴权语义。

独立创建方真的存在时，新增 `NewImageRuntimeServer`/`ImageRuntimeUnary`：固定证书SAN allowlist、TLS1.3、两方法白名单、无反射、无stream；默认不开。它断言的是受信服务身份而不是用户身份。不要扩展旧GovernanceSAN以允许所有下游领取凭证。

## 5. 现有 Resource 接入点

| 已有文件 | 本轮变化 | 禁止 |
|---|---|---|
| internal/conf/v1/conf.proto | Bootstrap追加image=3 + Image typed配置 | 改旧tag/全局框架升级 |
| 配置Validate的真实所在文件（IMG-00确认） | enabled时校验Image配置，disabled不要求新Secret/DB | 把Harbor网络探测作为Network全局启动前置 |
| cmd/ani-resource-service/governance.go | 装配Image并注册TenantImageService，合并关闭资源 | 将平台/运行Secret接口加到tenant listener |
| cmd/ani-resource-service/main.go | 增加image-migrate/image-admin互斥分支 | 改现有-migrate/ANI_NETWORK_MODE/SAN |
| cmd/ani-resource-service/app.go | 仅装配真实需要的内部port/生命周期 | 将Image API直接注册到未鉴权full入口 |
| sqlc.yaml/Makefile | 新entry和定向Image门禁 | 重写Network migration/checksum |

## 6. Governance 文件与方法

| 新增目标 | 方法/改动 |
|---|---|
| api/protos/catalog/service/v1/image.proto | 无tenant/public安全DTO |
| api/protos/admin/service/v1/i_image.proto | REST路由+鉴权OpenAPI说明 |
| api/buf.image.gen.yaml | 复制当前定向生成约定，固定插件，clean=false |
| app/admin/service/internal/data/image_client.go | ImageClientConfig；NewImageClient；ImageConfigFromEnv；trustedImageCall；对应11个下游方法；Close cleanup |
| app/admin/service/internal/service/image_service.go | NewImageService(client,ResourceTenantResolver)；trustedImageOperator；11个BFF方法；validateImageReply；mapImageError |
| app/admin/service/internal/service/image_views.go | wireImageSpace/Registration/Credential，公开DTO不转发tenant/内部Secret字段 |
| pkg/middleware/auth/image_policy.go | Image方法—IMAGE模块—permission映射；credential响应no-store及body脱敏按实际中间件入口接线 |
| 现有HTTP/gRPC注册/DI入口、模块权限seed（IMG-00绑定） | 注册ImageService，接入IMAGE枚举与权限，保持默认最小权限 |

复用已有ResourceTenantResolver，不在新文件重复定义。Governance保持Kratos v2，Resource保持v3。客户端只import Resource `api/image/v1` 生成契约，依赖实际已推送的精确SHA/pseudo-version；不引入Resource internal、不用本地replace，不将旧Network依赖重命名作为本轮附带工作。

## 7. 普通容器创建方（待绑定）

将下列方法落入IMG-00确认的**实际owner代码**，不能假称原来已经存在：

| 建议新增方法 | 责任/测试 |
|---|---|
| ResolveRegisteredImage(ctx,tenant,imageID,scope)(ResolvedImage,error) | 调用窄接口，校验返回tenant/id/scope/ref；用目标集群平台校验 |
| PrepareTenantImagePullSecret(ctx,tenant) error | 从真实租户环境解析namespace；内部取pull材料；拒绝同名异主Secret；持久化/校验generation |
| ApplyRegisteredImage(template,selection) error | 仅设置获授权digest引用、Secret引用、Always；用户不能覆写为他租户路径 |
| PersistResolvedImageIntent(ctx,intent) error | 创建业务自己的不可变执行意图，重试不重新跟随tag |

`PrepareTenantImagePullSecret`只在首次环境准备/发现缺失或代次不符时取材料，不在每个Pod创建新的Robot。测试driver的这些方法不等于正式owner已接入。

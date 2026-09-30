# Image 数据库设计：无 RLS

数据输入：[DDL](../../migrations/image/001_image.sql)、[sqlc 查询](../../internal/data/image/queries/)。实施与真实 PG 验证状态仅见[执行账本](../execution/status.md#image-mvp)。

## 1. 数据归属与迁移

选用同一 PostgreSQL 实例中的独立 `image` schema；可以与 Network 同 database，但 Image 有独立 owner/runtime 登录角色和专用连接池。部署希望独立 database 时保持同一 schema 和DSN接口，不为此增加存储产品选择层。

**不把 SQL 放进根 `migrations/*.sql`**。现有 `network_schema_version` 校验全部根 SQL 个数与checksum（R4），不能把镜像插入旧迁移序列。新增：

```text
migrations/image/001_image.sql
migrations/image/embed.go                  package imagemigrations，//go:embed *.sql
internal/data/image/migrate.go             Image 自己的迁移器
internal/data/image/queries/*.sql
internal/data/image/sqlcgen/*               Fedora 生成
```

`sqlc.yaml` 新增一个独立 entry，schema=migrations/image，queries=internal/data/image/queries，输出 internal/data/image/sqlcgen；uuid→string、timestamptz→time.Time、nullable→指针，沿用原约定。不让 Image 导入 internal/data/network 或其 sqlcgen。

同一二进制新增 `-image-migrate`，与 -migrate/-node-facts/平台管理命令互斥。Image runtime启动不能自动执行DDL。新迁移器独立锁定 Image schema_version，按有序文件checksum逐条短事务执行，已有版本checksum不符拒绝；不能删除旧版本记录或改历史迁移来修复。首次版本表bootstrap由迁移器与001协调：版本表不存在时执行001并在同一事务登记版本1；后续不重复CREATE旧表。

## 2. 四个业务表

| 表 | 主要字段/主键 | 职责 |
|---|---|---|
| image.spaces | space_id UUID；owner_scope；tenant_id；project_name/id；registry_authority；installation_id；state/version | 租户或平台与唯一 Harbor Project 的绑定 |
| image.credentials | PK(space_id,purpose)；scope/tenant；generation/version；Robot id/name/username；expiry；仅pull长期密文 | 两类逻辑凭证的有效绑定；历史候选不是新产品实体 |
| image.registrations | image_id；space/scope/tenant；repo/source_ref/rootdigest；platforms；purposes/accelerator；version；unregistered_at | 登记固定版本，不是完整Harbor制品索引 |
| image.commands | command_id；space/scope/tenant；idempotency_key；kind/actor/fingerprint；阶段/结果；候选ID与临时Secret密文 | 当前同步调用重试的耐久事实，不是通用作业表 |

`image.schema_version` 是迁移元信息，不是第五个业务领域对象。不建 Tenant/Membership/IAM 影子表，不添加跨服务外键，不将配额账本放入 Image。

## 3. 租户约束

租户行要求 owner_scope=tenant 且非空非零tenant UUID；平台行要求 owner_scope=platform 且tenant_id为NULL。NULL只表示**这行确实属于平台**，绝不作为查询权限通配符。

所有子表同时有 `(space_id,owner_scope)` 和 `(space_id,tenant_id)` 复合 FK，加scope/tenant CHECK：前者防止platform行关联tenant空间；后者防止tenantA行关联tenantB空间。仅增加 `tenant_id` 字段、仅用 space_id 外键或只在前端过滤均不合格。

普通tenant查询、更新、命令查找、凭证读取必须显式绑定 tenant。平台目录单独SELECT WHERE owner_scope='platform' AND tenant_id IS NULL。平台写另走Platform用例/CLI，不用一个参数可空的万能Repository方法。多租户批量运维SQL不暴露产品接口。

## 4. 唯一性与并发

每租户一个空间、单平台空间、同Registry Project地址唯一、Harbor ProjectID唯一。所有名字先规范化再入库，uniqueness conflict映射业务reason，不做先SELECT后裸INSERT的伪并发检查。

有效登记唯一(space,repository,digest)。同内容可以在不同tenant空间独立登记；不做跨租户Digest dedup资产权限。取消登记后同内容可新登记为新image_id；历史image_id不会复活。

更新采用 version CAS。affected=0后再做同tenant限定查询区分NotFound/Cancelled/VersionConflict，不查询全库后泄露其他tenant。

幂等键在同space内跨方法共用；同键不同kind/actor/fingerprint拒绝。外部副作用同space只能一个未终结command；目录DB事务可以独立串行/CAS，不等待所有Harbor管理操作。Harbor调用前后使用短事务更新阶段，HTTP不跨长PG事务。

Project 创建先保存 `project_sent` 再发请求。只有适配器确认的完整拒绝响应才允许将原 command 保存为 `retryable/project_rejected`；该阶段和原 reason 的持久化成功后，原 key 可重新检查绑定和同名对象再发送。`project_sent` 且无可信 Project ID 一律保留不确定性，不以查询404重置阶段。开放 command 唯一约束不变，不删除 command 或直接改库解除阻塞。

## 5. 必须实现的 sqlc 查询名

| 文件 | 查询名 |
|---|---|
| queries/spaces.sql | ReserveTenantSpace、GetTenantSpace、GetPlatformSpace、BindTenantProject、SetTenantSpaceAvailable、SetTenantSpaceReason；平台对应明确Platform名称的查询 |
| queries/credentials.sql | GetTenantPublisherCredential、GetTenantPullCredential、PutTenantCandidateState、ActivateTenantCredential、DisableTenantCredential；平台publisher对应查询 |
| queries/registrations.sql | InsertTenantRegistration、GetTenantRegistration、ListTenantRegistrations、UpdateTenantMetadata、UnregisterTenantRegistration；Insert/Get/List/Update/UnregisterPlatformRegistration |
| queries/commands.sql | GetTenantCommand、InsertTenantCommand、UpdateTenantCommandPhase、CompleteTenantCommand、FailTenantCommand、GetOpenTenantExternalCommand；明确平台对应查询；ScrubExpiredDeliverySecrets |

每个tenant方法tenant参数必填，不做“同一个查询tenant为NULL就查platform”。所有业务DML由sqlc生成；数据库catalog/readiness、角色校验、迁移基础设施SQL可在adapter中显式书写，沿用现有风格。

唯一project冲突检查可用平台管理查询，但不能返回对方tenant身份给调用者。Session advisory锁实现放data/image，使用单独acquired连接、锁释放/断链后丢弃连接、加锁等待上限；不引入跨域共享锁框架。

## 6. 查询分页

tenant和platform各自查询，以created_at DESC,image_id DESC排序，取limit+1。cursor是由biz签名的版本化payload，绑定资源tenantUUID、scope、规范化filters、limit、最后key；HMAC>=32字节密钥从Secret文件注入。tenantA的cursor在B下拒绝；更改filter也拒绝；枚举/长度非法先拒绝再访问DB。

search按字面量contains匹配，转义 `%`、`_`、`\\`，不能让用户构造查询。先WHERE所有filters再LIMIT，不先取页后在Go过滤。首期只有两个页签，不做租户+平台混合页及全局总数。

## 7. Secret 与命令字段约束

credentials.secret_ciphertext只允许pull长期保存，publisher的当前行不保留可恢复密码。签发交付密文保存在command，success后设置10分钟重放窗口；请求时检查actor、kind、指纹、当前generation、window，全部满足才解密。

command.candidate只存RobotID、候选代次、名称、ownership标识及可恢复阶段；result只存脱敏响应快照/对象ID，不含Secret/认证头/原始provider响应。PG只约束JSON为object，内容脱敏靠typed编码和“哨兵Secret不得出现”测试。payload/key/nonce验证失败按密文损坏拒绝，不能当不存在后重建新身份。

日志和SQL错误不可包含密文解密结果。密钥key_id支持读取已有版本，key文件权限0600或受控挂载；本轮不建设KMS服务或自动重加密后台。

## 8. 角色与readiness

Image owner拥有image schema/tables；runtime仅USAGE image、四张表SELECT/INSERT/UPDATE（本MVP不需硬DELETE）、schema_version SELECT。禁止runtime superuser/bypassrls/createdb/createrole/schema CREATE/数据库CREATE或TEMP/SET owner权限；不授予Network表，也不授予Image owner登录给服务。

迁移器使用已配置的owner DSN与runtime角色标识（标识符安全引用，不拼用户SQL），创建角色/授予登录由部署初始化承担；**不要在001中硬编码密码或改共享角色权限**。共享库发现public或继承角色仍可访问Network时，由部署授权审查修正专用角色，不能因表面grant少就声称隔离。

`CheckReady`只检查Image的表、版本、checksum、角色和RLS关闭；RLS误启应报错，不自动关闭。Network迁移运行前后bytes/checksum/version/旧客户端验证保持一致。Image关闭时不要求新schema存在。

## 9. 必须的真DB反例

A的registration写入B的space必须FK失败；platform子行绑定tenant space必须失败；A Get/Update/Unregister/command/pullcredential不能访问B；同租户不同space复用幂等键不可串单；两并发slug争抢仅一方成功；并发相同command仅一个真实副作用推进；metadata CAS失败不能覆盖；runtime执行DDL、SET owner、读Network测试表失败；表RLS均false。

mutation测试从隔离源码副本移除某个tenant谓词或复合FK，原负向测试必须失败，恢复源码并核对hash；不能把违反隔离的变异版本部署到共享环境。

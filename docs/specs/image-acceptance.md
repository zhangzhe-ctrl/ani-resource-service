# 验收矩阵

每项保存测试源文件/命令、执行SHA、Fedora退出码和证据；mock只用于对应isolated级，不能替代live。正向演示只需轻量CPU镜像，不要求GPU驱动/训练框架作为基础镜像闭环前置。

| ID | 场景 | 通过标准 | 等级 |
|---|---|---|---|
| A01 | Image disabled | 旧Network配置、API、迁移和启动门禁无行为变化 | isolated |
| A02 | 无caller/错误SAN/重复身份metadata | 请求在数据访问或Harbor调用前拒绝 | isolated |
| A03 | JWT/AK租户映射 | 以可信uint32映射资源UUID，用户输入UUID不能覆盖 | isolated+live |
| A04 | tenant mutation | A写B的space复合FK失败；移除tenant谓词的变异使测试失败 | 真PG |
| A05 | 平台行关系 | platform子行绑tenant空间失败；空tenant无超级查询含义 | 真PG |
| A06 | role/readiness | runtime DDL/SET owner/越权Network读失败；RLS误开拒绝ready | 真PG |
| A07 | migration | 首建、校验重跑通过；Image改旧checksum失败；Networkroot文件/hash/version不变 | 真PG |
| A08 | slug竞争 | 同租户重复同slug不重复Project；两tenant争slug仅一成功 | isolated+live |
| A09 | 初始化半失败 | ProjectID已绑定/Robot创建/Secret设置后断开可同键恢复，无跨tenant认领 | isolated+live受控 |
| A10 | unknown project | 无可信ID且同名已存在时blocked，不因同名采用 | isolated |
| A11 | 管理身份权限 | actual版本可创建目标Robot；子权限不足明确失败，不自动扩权 | live |
| A12 | publisher与pull范围 | publisher本tenant Push/Pull；pull仅Pull；B和platform写都拒绝 | live |
| A13 | 完整Robot用户名 | 自定义prefix仍使用Harbor返回用户名登录成功 | isolated；有授权时live |
| A14 | Secret密文 | 换AAD/换tenant/篡改cipher/缺key拒绝；普通GET/日志/错误/DB JSON无Secret哨兵 | isolated+真PG |
| A15 | 凭证交付重试 | 同键同actor同参数当前代次10分钟内同结果；异actor/过期/旧代次不能回取 | isolated |
| A16 | 迟到凭证请求 | 旧command不刷新新generation；重复SetSecret只作用原候选，旧身份停用幂等 | isolated |
| A17 | 停用 | 新登录/新Token获取受限；已有Token按实际TTL测试，不声称瞬间撤销 | live |
| A18 | ref安全 | foreign host、userinfo、encodedslash、path traversal、other tenant project拒绝，无SSRF | isolated |
| A19 | runnable artifact | 标准manifest/index平台可解析；附件/模型/chart不作为容器；大小/深度限额生效 | isolated+live |
| A20 | 固定Digest | 推送tag-v1登记后重推同tag，原登记和已接受任务仍用原Digest | live |
| A21 | filter与cursor | 过滤发生在分页前；多页无漏；cursor跨tenant/scope/filter拒绝 | 真PG |
| A22 | 乐观并发 | 同version两个更新仅一成功；不能经元数据改owner/repo/digest | 真PG |
| A23 | unregister | 目录取消不发Harbor DELETE；后续新建拒绝旧登记；已运行任务保持 | live |
| A24 | Harbor故障 | 查询/解析返回503或合理原因，不清空DB；已存在业务不被错误删掉 | isolated+live受控 |
| A25 | 内部runtime接口 | 不在公共HTTP/租户RPC白名单；错误服务证书失败；返回材料只含该tenantPull身份 | isolated+live |
| A26 | Secret namespace与ownership | Secret在实际tenant NS；同名异主拒绝覆盖；B Secret不可拉A | live |
| A27 | 实际产品创建 | 通过已存在container API创建；PodTemplate使用登记ref/Secret/Always；容器输出本次唯一标记 | product |
| A28 | index/runtime digest解释 | Pod.spec.image匹配根Digest；imageID若报告子manifest/config按运行时语义核验，不用字符串不等误判 | live |
| A29 | 共享节点缓存 | B不能借A预拉缓存启动A镜像；其他Pod创建入口的准入边界有证据 | live |
| A30 | UI | 真实API列表/登记/凭证弹窗/选择创建；Secret未进入localStorage/store/URL/错误收集 | product |
| A31 | 回归 | 既有verify/integration/race/tenant-mutations和新增Image用例真实执行；非零不能吞 | Fedora |
| A32 | 限域清理 | 只删本run精确ID/UID对象；无全局GC/共享对象删除/密码变化 | live |

## 产品验收的必要证据

记录用户操作或API请求→Governance请求标识→Resource登记ID与Digest→真实业务owner创建ID→PodUID/spec.image/imagePullSecrets→容器输出。不要收集用户密码、Secret内容、原始Authorization或完整dockerconfigjson。

两个tenant必须有真实不同Registry凭证。隔离负例包含直接客户端访问（不是只隐藏列表）、错误Image ID、错误namespace、只读凭证尝试Push。对缓存负例，受控驱动只能验证所配置的入口；若租户还能直连其他不受约束的Pod入口，必须记录未满足的边界，不能写“全平台隔离完成”。

一个成功的`kubectl run`或测试driver生成的Pod仅证明技术链路，不证明产品owner、权限、UI和业务持久化已经接通。找不到前端/owner时仍交付后端证据，但MVP总状态不能PASS。

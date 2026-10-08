# 2026-09-30 三节点真实镜像拉取

用户明确允许自行创建测试租户 Namespace 和拉取 Pod。本次完成隔离技术验收，未接入普通容器业务 API、Governance/IAM 租户或前端。唯一当前进度仍见 [status](../../../../status.md#image-mvp)。

## 输入与执行

- Resource 工作树为 `de8fc3d4324e171631b9a31a45c62a0ca9a61698`，产品运行源码仍为已验 `62ecc67b839dd1040468323f8c316189a0c30653`。本次不启动或修改产品服务，不用本次证据替代其真实链路验收。
- 提交前对上述精确 de8fc3d 再次运行 Fedora `make verify`，退出 0：[命令记录](source-verify.json)、[完整输出](source-verify-de8fc3d.txt)。后续只追加本记录与执行状态；旧全量数据库/race 证据不被此次普通 verify 替代。
- API 驱动、镜像获取与推送全部在 Fedora，运行目录 `/home/chabking/ani-image-mvp-runs/pod-pull-20260930T0200Z`。本地只编辑运维驱动和转运；执行原件见 [preflight](preflight.py.txt)、[driver](driver.py.txt)、[cleanup](cleanup.py.txt)，不是新的产品入口或可重复使用的通用部署脚本。
- 使用既有 `net05a-heavy.lock`，`systemd-run --user --scope -p CPUQuota=200% -p MemoryMax=2300M -p MemorySwapMax=0`；主驱动 `timeout -s INT 1200 python3 .../driver.py`，shell `pipefail` 保留非零。
- 写入前固定 [profile](profile.json)：cluster UID `87ecef8e-ac4e-442b-8e15-5e906263be6b`，ani-01/02/03 即 `172.16.101.10～12`；Harbor HTTPS `172.16.101.10:30003`，认证读取实际版本 `v2.15.2-a97e7b83`。
- Kubernetes 经本地 SSH 和 Fedora 反向转发连接仅监听 loopback 的节点 kubectl proxy，后者使用节点原有 admin.conf 校验 API TLS；未复制集群客户端私钥。临时后端管理凭证从已确认的 Harbor admin Secret 读取，未进入 Pod、命令参数、Git 或日志。两个测试 tenant UUID 仅标识测试夹具，不冒充 IAM 租户。
- Harbor CA PEM SHA256 `59e87314e44f89c3c562bdaa0535da00b45567711bd013a1ae268299406e7b28`；curl 使用 `--cacert`，skopeo 使用专属 CA 目录，未使用 `-k`、insecure registry 或关闭 TLS 验证。
- 基础镜像 BusyBox 1.37.0 的源 index 与实际 amd64 manifest 分别固定在 [base-image](base-image.json) 和 [manifest](manifest-a.txt)。本次传入 Pod 的 manifest digest 为 `sha256:66a6306db78bf2dbf3487f293aa8d6990d8e506fdffab9cc43fe422becf886e4`；总镜像文件小于 32 MiB。

## 实测结果

主驱动执行于 `2026-09-30T02:08:44Z～02:09:42Z`。完整资源 ID/UID、权限和断言见 [result](result.json)。

| 检查 | 结果与边界 |
|---|---|
| 三个 Private Project | 创建 ID 3/4/5；分别为测试 A、B、共享平台；没有采用同名旧对象 |
| 五个有限期 Robot | ID 4～8；三个各自 Project 的 Pull/Push 发布身份，两个只能读取自身及测试共享 Project 的运行身份；权限读回与请求一致 |
| 固定 digest 发布 | 三个 Project 的 manifest SHA256 均与源 amd64 manifest 相同 |
| 写入负例 | 运行身份向 A Push 被拒绝；A 发布身份向 B、共享平台 Push 均被拒绝，保留实际 unauthorized 响应 |
| 正向 Pod | A 镜像在三节点分别 Ready/Running；B 自有镜像和 B 读取平台镜像成功，共 5 个 Pod |
| 已缓存跨租户负例 | 先确认三节点 A Pod 启动，再在每节点使用 B 运行身份读取相同 A ref；3 个 Pod 均因 registry HEAD 返回 401 而无法启动 |
| Pod 约束 | 固定 digest、`imagePullPolicy: Always`、Namespace 内拉取 Secret、不挂载 SA token、非 root、只读根文件系统、无提权；每个 Pod 上限 100m/64Mi，两个 Namespace 各有 5 Pod 配额，无 PVC |
| runtime imageID | digest 均对应本次 amd64 manifest；部分 imageID 使用同 digest 的其他仓库别名，不能仅按仓库路径不同判失败；不覆盖多架构 index 的所有运行时情况 |

这是 A11/A12/A26/A28/A29 的部分真实基础设施证据，不能把这些完整验收项全部改为 PASS：未通过产品签发和解析接口，未测试 Secret 异主覆盖、停用 Token TTL、其他 Pod 创建入口或集群级强制 Always 准入。共享缓存负例仅覆盖本次明确设置 Always 的 Pod。没有读取容器日志来验证产品唯一输出标记，不能冒称 A27。

## 首次清理失败及恢复

测试断言通过，但主驱动退出 **1**：Namespace/Pod/Secret/Quota 与 Robot 已删除，仓库清理使用数字 Project ID 的子资源路径返回 404，因此保留三个 Project，没有扩大删除范围。

[cleanup-recovery](cleanup-recovery.json) 改用 Project 名称访问 repositories；每次先按已记录数字 ID、名称和 creation_time 核对对象，只删除本次 `fixture` 仓库内已记录的 digest。恢复退出 **0**，三个 Project、五个 Robot、两个 Namespace 均独立读回 404；8 个 Pod 和两个 Secret 随所属 Namespace 确认不存在。未触发 GC、删除共享卷、改密码或修改共享 CA/准入设置。

所有临时凭证文件已删除，节点代理和两条 SSH 转发已关闭。原始非零结果未重写；[closure](closure.json) 区分 preflight=0、driver=1、cleanup recovery=0。[SHA256SUMS](SHA256SUMS) 对应 Fedora 回传白名单；本地逐文件核验通过。

## CA 与驱动兼容问题

`Key Usage` 是 X.509 证书扩展，不是 Harbor 的功能开关。当前自签 CA 有 `CA:TRUE`，但没有 Key Usage；Fedora Python 3.14 的 `create_default_context()` 严格验证报 code 92。Python 从 3.13 起默认启用 `VERIFY_X509_STRICT`，可能拒绝旧式或不完整证书；这不是 Image 业务代码额外实现的检查。[Python 官方说明](https://docs.python.org/3.14/library/ssl.html#ssl.create_default_context)

Harbor 的 HTTPS 配置消费证书；官方示例中生成 CA 的命令没有显式指定 Key Usage，而服务端证书扩展单独指定了该字段，实际 CA 输出还受 OpenSSL 配置影响。因此不能把该问题描述为“Harbor 通常需要打开某个开关”。本次 curl、skopeo、节点 containerd 在继续校验证书链和地址的情况下完成真实访问。[Harbor 官方说明](https://goharbor.io/docs/main/install-config/configure-https/)

没有修改共享 CA，也没有降低原 `scripts/image_smoke.py` 的验证策略。原完整 smoke 仍有两个已知前置问题：Python 严格 CA 验证；匿名 systeminfo 不提供 harbor_version，而已认证读取提供实际版本。该驱动及完整产品 live profile 仍需后续处理，不能将本次运维驱动通过写成原 smoke 通过。

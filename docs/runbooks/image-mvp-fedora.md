# Image MVP：本地编码、Fedora 重任务执行手册

## 1. 本次唯一执行规则

本地只做源码/文档编辑、静态文本查阅、diff审阅、Git commit/push/fetch及SSH/SCP转运。不运行Go依赖解析、go list、gofmt、Buf/sqlc/protoc、编译、测试、Docker/Podman构建、数据库、live API驱动或前端依赖安装/构建。即使历史remote-execution.md存在Ubuntu或本地兜底，本次也不得启用。

所有上述工作经 `ssh fedora` 执行。Fedora不手写/修补业务源码，不commit/push产品仓库；只按指定完整SHA检出、生成、格式化和验证。失败修复回本地新提交，绝不在远端修好后用未提交状态验收。

本次任务包的文本/ZIP生成不是产品测试；本包没有附带“Fedora已通过”结论。

## 2. 运行目录与输入

在远端读取真实HOME，不改HOME。新建专属目录，例如 `$HOME/ani-image-mvp-runs/<run-id>/`，目录权限0700；具体run-id由执行时生成并写入记录。不要复用历史rsmod目录或任何未证实归属的路径。

```text
<run-root>/
  repo/resource/<full-sha>/             精确SHA源码副本
  repo/governance/<full-sha>/
  repo/consumer/<full-sha>/             仅已绑定时
  inputs/                              0600，运行配置/授权profile，不进Git
  cache/{go-build,go-mod,gopath,tools}/
  generated-return/                    仅生成物/格式化差异及清单
  evidence/<batch>/<attempt>/           脱敏日志/退出码/指纹
  state/                               当前会话恢复checkpoint（不是产品当前进度权威）
```

GitHub读取SHA仅是参考，IMG-00重新核对执行分支；不自动切回旧基线或覆盖用户未提交改动。source-lock中记录基准SHA和每次候选SHA；合同、源码、生成物所验SHA一致。

live profile示例见templates。**Secret只有路径，不把明文塞进JSON、命令参数、聊天或报告**。未知host、kubecontext、工作负载repo/方法用null/blocked，不编造默认地址或在GitHub搜索到同名就当现网。

## 3. 重任务互斥与预算

复用当前项目登记的Fedora重任务锁。R9历史位置是 `/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock`，IMG-00核对当前是否仍有效；不得因为服务改名就另建平行锁绕开其他任务。锁占用就等待/记录阻塞，不杀掉占锁进程。

沿用已有预算上限：CPUQuota=200%，MemoryMax=2300M，MemorySwapMax=0；GOMAXPROCS=2，GOFLAGS=-p=2，GOMEMLIMIT=1536MiB，GOTOOLCHAIN=local，GOWORK=off。设置run专属GOCACHE/GOMODCACHE/GOPATH/工具目录而不更改全局profile或HOME。确认Go版本与锁定go.mod一致；没有工具时仅按当前授权在run目录准备固定版本，不能自动sudo改全机。

真PG一实例、约768MiB/1CPU、随机127.0.0.1端口、独立owner/runtime账号、测试最长20分钟；若PG容器不在主进程cgroup，要另设容器预算并记录实际合计，不能把子进程限制冒充远程Pod/容器的总限制。集群工作负载预算另按allowlist设置。

默认串行重验证，禁止为了加速同时在多仓启动无限go test/npm build/docker build。预算不够时停止当前命令、记录诊断并调整本run内测试分组，不自动增大限制或开启swap。

## 4. 每批源码循环

1. 本地阅读批次和当前状态，按小任务编辑允许文件；`git diff --check`等纯文本检查允许，所有语言工具到Fedora。
2. 创建正常源码提交并推到review分支，不推main。记录完整SHA，不用“latest/main”作为测试输入。
3. Fedora在本run全新/干净源码副本fetch指定SHA并detached checkout；校验HEAD等于输入、没有工作树改动；设置预算后运行生成/格式化。
4. 生成导致差异时，只回传明确白名单内生成文件/格式化变更，带每个原文件base SHA256、新SHA256和对应源SHA。不得打包.env、kubeconfig、inputs、cache或任意软链接目标。
5. 本地核对源SHA、原文件hash和白名单，再应用生成物；禁止直接scp覆盖整个工作区。产生新普通commit/push。
6. Fedora按新SHA重新生成，必须无差异，再跑定向测试。只生成成功、只pb.go存在都不是契约通过。
7. 重测试与live命令记录UTC开始/结束时间、host、cwd、sourceSHA、command（去Secret）、exit code、日志位置/hash、实际资源ID。`set -o pipefail`，tee不能吞失败，返回非零不得加`|| true`假绿。
8. 本地补充历史证据摘要与唯一status后正常commit/push。纯文档提交可引用相同代码树的已验SHA，但要明确最终文档commit与测试codeSHA关系；任何代码/配置改变要重新跑对应门禁。

以上是执行契约，不要求构建一套自动CI平台。Agent可以用现有SSH执行器按顺序操作，但不得把计划文档直接当已存在shell命令运行。

## 5. 真实环境授权

真实写入前，profile必须明确：集群API/CA或证书指纹、允许namespace/Project前缀、existing platform是否允许写、两测试tenant与账户来源、业务服务入口、镜像base digest、预算、允许cleanup。只有“SSH能连上”不等于授权修改任何可见集群。

第一次仍可自动建立**已获本profile授权**的run-owned Project/Namespace/Secret/Pod；不要求管理员去HarborUI。但不能认领同名旧对象，不能为了测试给默认ServiceAccount集群管理员权限。

Registry TLS必须由节点运行时信任：Secret只解决认证，不解决自签证书CA。现有CA/域名不通要报环境阻塞，不关闭TLS验证、不在所有节点写insecure registry。保留所有已有扫描、签名和准入门禁，不将测试用例改成使用admin万能Secret。

小镜像构建/Push在Fedora，base镜像使用已批准且可验证的Digest；不在文档捏造一个实际不存在的sha256。平台镜像测试优先用专属测试平台Project（配置中的platform_project），不覆写真实共享platform内容。

## 6. 状态、自动继续与失败恢复

唯一当前进度：`docs/execution/status.md`追加的Image区块。每批四维状态是pending/running/pass/blocked/failed/not_run/n_a；pass必须有证据链接，n_a必须说明为何不适用。历史evidence/result.json允许存快照，但不能与status竞争“当前真相”。

执行器默认继续下一个已满足依赖的小任务/批次；不逐批问“是否继续”。可自动修复明确属于本范围的错误并重试，但不得无限循环或改验收阈值。连续同因失败且无法用现有权限解决时写blocker，并做后续独立任务；没有可执行任务才停止。

会话容量或工具时限将到时：写入checkpoint，包含当前sourceSHA、小任务ID、已执行的原始命令与退出码、未完成副作用、下一安全操作、所需授权。不要声称会在会话结束后继续运行。重开会话先读唯一status、checkpoint及当前代码，不能盲目重跑已发出的Harbor写请求。

## 7. 清理与交付

只清理记录为本run创建、ID/UID与ownership均匹配的对象。删除namespace之前核对实际UID及成员清单；删除Harbor测试project前核对project ID、安装标识与精确测试清单；已注册产品API不提供镜像物理删除，测试cleanup是受控运维动作，不变成租户产品能力。

禁止全局GC、清空共享卷、清理其他人的未跟踪源码、git clean -fdx/force reset、杀未知进程、改现有密码。不能确认安全清理的资源保留并列出。

默认交付review分支和可审阅diff/证据；不自动merge/rebase/amend/force-push，不擅自正式发布或切换生产部署。

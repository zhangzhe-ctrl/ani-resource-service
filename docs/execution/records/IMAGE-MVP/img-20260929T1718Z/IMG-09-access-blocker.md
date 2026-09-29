# IMG-09 前端历史准备（后由用户排除）

2026-09-30 用户明确回复“你不做前端对接工作”。自此IMG-09/A30为用户排除的n_a，
以下只记录此前已发生的准备和失败，不再将前端权限作为待恢复阻塞或继续工作的请求。

候选实仓 `liangzai006/ani-console`，本地基线
`47233a7279c6ec563ea2842192379bf14546f6e8`、原checkout master clean。
已读该仓AGENTS/Console约定、冻结设计规范2.0、阶段记录和实际组件/API生成流程。
新实现使用独立worktree
`/home/chabking/workspace/.worktrees/console-image-mvp-20260930`，review分支
`codex/image-mvp-20260930`；原master未切换、未修改。

本地提交 `674f2f0b09be0faf2b36ff334f291ad2575e21d7` 仅准备3文件：
`frontends/console/scripts/import-image-contract.py` 选择固定Governance
`d64d6ee478801795afadb4573ba25f8c2de6b7fc` 的11项Image操作与权限码查询；
`gen-core-schema.mjs` 接入命名空间化的契约切片；`docs/sprints/SPRINT-P24-container-images.md`
保留批次过程和边界。没有生成contract/type，没有写目录页，没有跑npm验证，不能记code pass。

实际SSH推送命令（2026-09-29）：

```text
git -c core.sshCommand='ssh -o BatchMode=yes -o ConnectTimeout=12 -p 443 -o HostName=ssh.github.com' push -u git@github.com:liangzai006/ani-console.git codex/image-mvp-20260930
ERROR: Permission to liangzai006/ani-console.git denied to zhangzhe-ctrl.
exit 128
```

原配置HTTPS remote的普通 `GIT_TERMINAL_PROMPT=0 git push origin ...` 无返回，
核对本任务PID113421、cwd和完整argv后只停止该Git进程及其确认子进程；未杀未知进程。
随后SSH `ls-remote ... refs/heads/codex/image-mvp-20260930` exit0、无ref输出，未发现分支发布。
完整源码bundle已作为转运文件到Fedora inputs；**未在未推送SHA上生成/编译/测试**。

当前所缺是可写前端仓库或该仓对当前身份的write授权，已异步请求用户提供；不擅自fork到
其他账户发布或绕过用户规定的“先push，再Fedora验证”循环。获输入后先核对目标/baseline，
再推送准备提交、导入实际OpenAPI、生成类型和实现目录/弹窗/维护/E2E。

已确认的真实接线：`coreApi`基址 `/api/v1`、已有JWT/刷新逻辑；Arco/TanStack；
`src/routes/_authenticated/images/index.tsx` 是VM ISO页面，保持原义；拟新增
`/container-images` 目录。Governance权限码实端点 `/admin/v1/perm-codes`，字段 `codes`。
新页面须通过已有认证client读取并fail-closed，不能以角色名猜测写权限。
普通容器创建页是 `/instances/container/create`，其 `POST /instances` 后端owner和tenant
Namespace解析仍未绑定；IMG-09.6不能用测试Pod或新造页面代替。

# Harbor 适配器阅读基线

2026-09-29 Fedora 从官方固定 v2.15.0 下载用于源码核对，存于 run inputs/harbor-v2.15.0；不是实际部署版本核验。

- [OpenAPI](https://github.com/goharbor/harbor/blob/v2.15.0/api/v2.0/swagger.yaml)，SHA256 `0c5deed400ba534abcb29f76af61a2e1355d865fcf1157d54ce8ba9136ec89c1`。
- [Robot handler](https://github.com/goharbor/harbor/blob/v2.15.0/src/server/v2.0/handler/robot.go)，SHA256 `905c4237c4edbc64b7bb40aae273e97006938352cb562fe12c230d43450aa3c3`。
- [Robot model mapping](https://github.com/goharbor/harbor/blob/v2.15.0/src/server/v2.0/handler/model/robot.go)：返回完整 name，permissions、duration、editable。

实现约束：Project metadata.public 用字符串 false；POST Location 要求 ID，丢失不可同名认领。Robot 创建后随机 secret 不交付，稳定 secret 用 PATCH /robots/{id} 设置且不依赖回显。PUT 停用保留原完整身份/权限/期限，写前后 GET 校验。请求为 system level 以覆盖两个明确 project，但 permissions 只有 project/repository push/pull，无 system permission 和全项目授权。部署须显式提供实际 robot 名称前缀用于精确查找；交付用户名始终取 Harbor 返回值。

Artifact API 的 manifest_media_type、type、extra_attrs、references.child_digest/platform/annotations 用于有限容器平台解析；不访问 layer、addition_links 或外部 URL。httptest 是协议/故障隔离证据，不是实版 Harbor 接入证据。

额外字段核对：[manifest processor](https://github.com/goharbor/harbor/blob/v2.15.0/src/controller/artifact/processor/image/manifest_v2.go) 输出 extra_attrs.os/architecture，SHA256 `5adc8b5c534c066fe5c5c32b66ab17978f3915cd5071348f690d64b3fc7d53a7`；[index processor](https://github.com/goharbor/harbor/blob/v2.15.0/src/controller/artifact/processor/image/index.go) 为 IMAGE，SHA256 `7f18632969a514d6bdd8d24b9c188a497e5c63055a1c50937b2f0fe9cfec3e14`。Robot model SHA256 `0e2d1f8e534761d964eb54622c81da562624c3879946b3db146c2bc6bf65df14`。

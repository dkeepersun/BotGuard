# 接入示例

- [`demo/`](demo/README.md)：Docker 一键演示。包含浏览器组件、PHP SDK、gateway、MySQL、Redis，以及 16 个攻击、误用和风险场景（含滑块与无障碍备用路径）。

接入顺序在所有语言里都一样：

1. 普通字段校验；
2. 业务身份和权限校验；
3. 用固定的 action 和 request_id 调用 verify；
4. 只有“通过”才执行本地幂等业务事务。

浏览器 token 本身不代表验证通过，验证通过也不能代替业务授权。

待补充：Go 业务处理器示例、Laravel 示例。

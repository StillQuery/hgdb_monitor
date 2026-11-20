# 已知限制

明确当前不支持的场景：

- 暂不支持 Harness Web 服务绑定非 loopback 地址：插件不会加载（管理 API 要求 loopback）。
- 暂不支持 headless / SDK / ACP 等无 Web 服务的 profile：插件不加载，AI 工具也不注册。
- 暂不支持私有 CA / 客户端证书（mTLS）：仅提供 disable / allow / prefer / require 四种 sslmode。
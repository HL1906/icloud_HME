# iCloud Hide My Email API 文档

## 概述

HTTP JSON API，所有接口返回统一格式：

```json
{
  "success": true,
  "data": {}
}
```

**失败响应：**

```json
{
  "success": false,
  "code": "VALIDATION_ERROR",
  "message": "参数错误"
}
```

**稳定错误码：** `AUTH_REQUIRED`、`INVALID_CREDENTIALS`、`RATE_LIMITED`、`CSRF_INVALID`、`VALIDATION_ERROR`、`ACCOUNT_NOT_FOUND`、`OTP_REQUIRED`、`OTP_INVALID`、`UPSTREAM_UNAUTHORIZED`、`UPSTREAM_FAILURE`、`INTERNAL_ERROR`、`STORAGE_FAILURE`、`GROUP_NOT_FOUND`、`GROUP_NAME_TAKEN`、`POOL_UNAVAILABLE`、`POOL_NOT_CONFIGURED`、`POOL_BUSY`

**安全约定：**

- 除 `POST /api/auth/login` 与 `GET /api/auth/session` 外，所有 `/api` 接口都需要管理员会话
- 非 GET/HEAD/OPTIONS 请求必须携带 `X-CSRF-Token` 请求头
- 会话 Cookie：`hme_session`，`Path=/`、`HttpOnly`、`SameSite=Strict`；TLS 部署时设置 `ICLOUD_HME_SECURE_COOKIE=true` 启用 `Secure`
- 任何账号响应**绝不包含** `cookies`、`app_password`、`proxy` 字段（代理只暴露 `has_proxy` 布尔值）
- 用户可见错误消息不拼接上游响应体或秘密

---

## 认证端点

### 1. 登录

```http
POST /api/auth/login
Content-Type: application/json

{"password": "管理员密码"}
```

**成功响应：** 设置 `hme_session` Cookie

```json
{
  "success": true,
  "data": {
    "csrf_token": "随机值",
    "expires_at": "2026-08-05T22:00:00+08:00"
  }
}
```

**错误：**

- `401 INVALID_CREDENTIALS` — 密码错误（不设置 Cookie）
- `429 RATE_LIMITED` — 同一 IP 15 分钟内失败超过 5 次，响应带 `Retry-After` 头

### 2. 查询会话

```http
GET /api/auth/session
Cookie: hme_session=...
```

**成功响应：** 同上（csrf_token / expires_at）。会话无效返回 `401 AUTH_REQUIRED`。

### 3. 退出

```http
POST /api/auth/logout
Cookie: hme_session=...
X-CSRF-Token: <token>

{"success": true, "data": {"logged_out": true}}
```

### 3.1. 修改密码

```http
POST /api/auth/password
Cookie: hme_session=...
X-CSRF-Token: <token>
Content-Type: application/json

{
  "old_password": "当前密码",
  "new_password": "新密码"
}
```

**响应：**

```json
{"success": true, "data": {"changed": true}}
```

规则与安全行为：

- 必须同时通过会话校验与 CSRF 校验
- **必须提供当前密码**，防止会话被劫持后直接改密码锁死管理员
- 新密码至少 8 位；需「字母+数字」或长度 ≥16
- 新密码不能与当前密码相同
- 旧密码错误返回 `401 INVALID_CREDENTIALS`，且计入独立限流
- 修改成功后**注销其它所有会话**，当前会话保持登录
- 新密码经 argon2id 派生后写入 `data/admin_credential.json`（仅存哈希），重启后仍然有效

---

## 账号端点

### 4. 列出账号

```http
GET /api/accounts
```

**响应：** `Summary[]`，排序为 active → validating → pending → error，同状态按 name、id。

```json
{
  "success": true,
  "data": [
    {
      "id": "acc_12345678",
      "name": "主号",
      "real_email": "owner@example.com",
      "icloud_email": "owner@icloud.com",
      "host": "icloud.com",
      "status": "active",
      "alias_total": 15,
      "alias_active": 12,
      "has_cookies": true,
      "has_app_password": true,
      "has_proxy": false,
      "last_validated": "2026-08-04T09:00:00+08:00",
      "status_message": "",
      "created_at": "2026-08-01T09:00:00+08:00"
    }
  ]
}
```

**禁止出现的字段：** `cookies`、`app_password`、`proxy`。`status_message` 只映射固定文案（validating → "正在校验凭据…"，pending → "等待配置或验证凭据"，error → "凭据验证失败"），不返回内部错误原文。

### 5. 添加账号

```http
POST /api/accounts
X-CSRF-Token: <token>

{
  "name": "新账号",
  "icloud_email": "owner@icloud.com",
  "host": "icloud.com",
  "proxy": "http://user:pass@host:port",
  "cookies": "X-APPLE-WEBAUTH-TOKEN=abc; X-APPLE-WEBAUTH-USER=def"
}
```

- `name` 必填，去空白后 1–64 字符
- `icloud_email` 必填，`net/mail` 校验且地址值必须等于输入
- `host` 只能是 `icloud.com` 或 `icloud.com.cn`（默认 `icloud.com`）
- `proxy` 可选，必须是 `http`/`https`/`socks5` URL
- `cookies` 可选，支持 Cookie Header 字符串或 JSON 文本
- 无 Cookie 时状态为 `pending`，不访问网络
- 有 Cookie 时状态为 `validating`：凭据的**输入校验同步完成**（格式错误立即返回 `400`），
  但访问 iCloud 的会话校验在后台进行，接口不再等待上游（此前最坏会阻塞 >90s）
- 校验结束后状态自动变为 `active` 或 `error`，客户端可轮询 `GET /api/accounts` 获取结果
- 进程重启时残留的 `validating` 会被回收为 `pending`

**成功响应：** `201`，返回 `Summary`，其中 `status` 为 `validating` 或 `pending`。

### 6. 编辑账号基本信息

```http
PATCH /api/accounts/:id
X-CSRF-Token: <token>

{"name": "新名称", "host": "icloud.com.cn"}
```

只接受可选的 `name`、`icloud_email`、`host`，至少一个字段存在。响应返回更新后的 `Summary`。账号不存在返回 `404 ACCOUNT_NOT_FOUND`。

### 7. 更新代理

```http
PUT /api/accounts/:id/proxy
X-CSRF-Token: <token>

{"proxy": "http://user:pass@host:port"}
```

空字符串表示清除代理。响应只返回更新后的 `Summary`（代理值从不回显）。

### 8. 更新 Cookie

```http
PUT /api/accounts/:id/cookies
X-CSRF-Token: <token>

{"cookies": "a=1; b=2"}
```

`cookies` 同时兼容字符串与对象：

```json
{"cookies": {"a": "1", "b": "2"}}
```

两种输入最终都交给 `account.ParseCookieInput`。响应只返回更新后的 `Summary`。

- 与添加账号一致：Cookie 先落盘，状态置为 `validating`，会话校验在后台进行
- 校验完成后状态变为 `active` 或 `error`，客户端轮询 `GET /api/accounts` 即可
- 若校验进行中又换了 Cookie，当前轮结果按旧凭据丢弃，服务端会自动补跑一轮

### 9. 设置 App 专用密码

```http
POST /api/accounts/:id/password
X-CSRF-Token: <token>

{"icloud_email": "your_email@icloud.com", "app_password": "xxxx-xxxx-xxxx-xxxx"}
```

服务端会用 IMAP 连接验证凭据。成功返回 `Summary`；IMAP 验证失败返回 `502 UPSTREAM_FAILURE`。

### 10. iCloud 密码登录（获取 Cookie）

```http
POST /api/accounts/:id/login
X-CSRF-Token: <token>

{"password": "用户的常规iCloud密码", "otp_code": "123456"}
```

- `otp_code` 可选，启用 2FA 时使用
- 需要 OTP：`409 OTP_REQUIRED`
- 验证码错误：`401 OTP_INVALID`
- 成功：**只返回 `Summary`，绝不返回 Cookies**（Cookie 自动持久化到账号配置）

### 11. 删除账号

```http
DELETE /api/accounts/:id
X-CSRF-Token: <token>
```

**响应：** `{"id": "acc_3"}`。不存在返回 `404 ACCOUNT_NOT_FOUND`。
落盘失败返回 `500 STORAGE_FAILURE`，此时账号**未被删除**（服务端会回滚内存状态），可重试。

---

## 业务端点

### 12. 创建 HME 别名

```http
POST /api/create
X-CSRF-Token: <token>

{"account_id": "acc_1", "label": "注册某网站", "group_id": "g_7f3a1c"}
```

- `account_id` 必填
- `label` 可选，最长 200 字符。**这是 iCloud 原生字段**：传空时 iCloud 会记为 `Created 2026-10-21 17:05`；写入后不可修改。
- `group_id` 可选，本系统的分组 ID（见「分组」一节）。分组不会写到 iCloud；先校验存在，再创建别名，避免白耗一个别名额度。

**响应：**

```json
{
  "success": true,
  "data": {
    "email": "xyz123@icloud.com",
    "label": "注册某网站",
    "created_at": "2026-01-15T10:30:00+08:00",
    "account_id": "acc_1",
    "group_id": "g_7f3a1c"
  }
}
```

新创建的别名在 iCloud 侧**默认就是启用状态**，无需额外调用激活接口。`created_at` 是调用创建接口的当时时间（RFC3339）。

**建号限流：** 每个账号每分钟最多 10 个，超出返回 `429 RATE_LIMITED`：

```json
{"success": false, "code": "RATE_LIMITED", "message": "建号太频繁了，请 23 秒后再试（每分钟最多 10 个）"}
```

响应头带 `Retry-After: 23`（秒）。限流按**账号**计（不是按 IP）：换 IP 重试没有意义，因为会被 iCloud 风控的是账号本身。参数校验失败的请求**不占用**额度。

### 12.1 健康检查

```http
GET /api/healthz
```

公开接口，无需登录，只回答"这个进程还能不能干活"，不返回账号数量、版本号等可被侦察的信息。

```json
{"status": "ok", "uptime_seconds": 3600}
```

数据目录不可写时返回 `503`，因为那种状态下别名元数据与转发标记都会写失败：

```json
{"status": "degraded", "uptime_seconds": 3600, "reason": "数据目录不可写"}
```

判定方式是**真的往数据目录写一个临时文件**再删掉：只读挂载、磁盘满、权限被回收都能被发现，而单纯 `stat` 看不出来。适合直接接 systemd / Docker / 反向代理的探活。

### 13. 读取邮件

```http
GET /api/inbox?account_id=acc_1&alias=xyz123@icloud.com&limit=20&days=7
```

- `account_id` 必填，且**必须是真实存在的账号**，否则 `404 ACCOUNT_NOT_FOUND`（不会把不存在的 ID 透传给上游）
- `alias` 可选，只返回发给该别名的邮件；传了就必须**属于该账号**，否则 `404 ALIAS_NOT_FOUND`
- `limit` 1–100（默认 20）
- `days` 1–90（默认 7）；非法整数直接 `400 VALIDATION_ERROR`

**响应（IMAP 优先，Web API 回退）：**

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "alias": "xyz123@icloud.com",
    "count": 2,
    "method": "imap",
    "messages": [
      {
        "id": "1042",
        "from": "GitHub <noreply@github.com>",
        "to": "xyz123@icloud.com",
        "subject": "[GitHub] Please verify your email address",
        "date": "2026-07-09T14:32:10+08:00",
        "preview": "Almost done! To finish setting up your account..."
      }
    ]
  }
}
```

`method` 为 `imap` 或 `web_api`。IMAP 路径支持服务端按收件人搜索；Web API 路径拉取后本地过滤。

> **注意**：IMAP 模式下列表只为生成预览拉取正文前缀（前 4 KB），不再下载整封正文，
> 因此高频轮询的流量开销大幅降低。需要完整正文请调用下方的「读取邮件详情」。

### 13.1. 读取邮件详情

```http
GET /api/inbox/1042?account_id=acc_1
```

- 邮件 ID 为不透明字符串：IMAP 模式下是 UID（纯数字），Web API 模式下是 `threadId`
- 两种 ID 都支持，不再因为「不是数字」直接返回 400

**响应：**

```json
{
  "success": true,
  "data": {
    "id": "1042",
    "from": "noreply@github.com",
    "to": "xyz123@icloud.com",
    "subject": "验证你的邮箱",
    "date": "2026-07-09T14:32:10+08:00",
    "preview": "…",
    "body": "完整的正文纯文本",
    "content_type": "text/html"
  }
}
```

### 13.2. 删除邮件

```http
DELETE /api/inbox/1042?account_id=acc_1
```

仅 IMAP 模式下可用。若邮件 ID 来自 Web API（非数字 `threadId`），返回
`400 UNSUPPORTED_IN_WEB_MODE`。

### 13.3. 转发服务：拉取待转发邮件

面向「隐藏邮箱分发给多人、再由服务按别名转发」的场景。与「读取邮件」的区别：

- `alias` **必填**（按别名路由给不同的人）
- 默认只返回**尚未标记为已转发**的邮件，从源头避免重复转发
- 可选直接带回正文，省掉一次详情请求

```http
GET /api/forward?account_id=acc_1&alias=xyz123@icloud.com&limit=20&days=7&include_body=1
```

| 参数 | 说明 |
|---|---|
| `account_id` | 必填 |
| `alias` | 必填，只取投递到该别名的邮件 |
| `limit` | 1–100（默认 20） |
| `days` | 1–90（默认 7） |
| `include_body` | `1`/`true` 时同时返回正文 |

**响应：**

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "alias": "xyz123@icloud.com",
    "count": 1,
    "method": "imap",
    "messages": [
      {
        "id": "1042",
        "from": "noreply@example.com",
        "to": "xyz123@icloud.com",
        "subject": "欢迎注册",
        "date": "2026-07-09T14:32:10+08:00",
        "preview": "感谢您的注册…",
        "body": "（仅 include_body=1 时返回）",
        "forwarded": false
      }
    ]
  }
}
```

### 13.4. 转发服务：登记已转发

拿到待转发邮件并成功转发后调用，下次轮询不会再返回这批邮件。

```http
POST /api/forward/ack
Content-Type: application/json
X-CSRF-Token: <csrf_token>

{
  "account_id": "acc_1",
  "alias": "xyz123@icloud.com",
  "ids": ["1042", "1043"],
  "forwarded": true
}
```

- `ids` 必填，单次最多 200 个
- `forwarded` 省略时默认 `true`；传 `false` 可**取消**标记（转发失败需要重试时用）
- 标记持久化在数据目录的 `forwarded.json`，按「账号 + 别名 + 邮件 ID」去重，别名大小写不敏感

**响应：**

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "alias": "xyz123@icloud.com",
    "updated": 2,
    "forwarded": true
  }
}
```

**转发服务的典型轮询顺序：**

```bash
# 1. 拉取还没转发的邮件
curl -b jar -H "X-CSRF-Token: $CSRF" \
  "http://localhost:8081/api/forward?account_id=acc_1&alias=xyz123@icloud.com&include_body=1"

# 2. 自己把邮件转发给对应的人…

# 3. 转发成功后登记,避免下一轮重复转发
curl -b jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"account_id":"acc_1","alias":"xyz123@icloud.com","ids":["1042"]}' \
  http://localhost:8081/api/forward/ack
```

### 14. 列出别名

```http
GET /api/aliases?account_id=acc_1
```

**响应：** alias 对象字段风格为 camelCase（兼容 iCloud 原始格式）：

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "count": 15,
    "aliases": [
      {
        "email": "xyz123@icloud.com",
        "anonymousId": "abc123",
        "label": "注册某网站",
        "active": true,
        "createdAt": "2026-01-15T10:30:00Z"
      }
    ],
    "meta": {
      "xyz123@icloud.com": {
        "email": "xyz123@icloud.com",
        "account_id": "acc_1",
        "group_id": "g_7f3a1c",
        "note": "注册试用",
        "updated_at": "2026-10-21T17:00:00+08:00"
      }
    }
  }
}
```

`meta` 是本系统的本地元数据（分组/备注），key 为小写邮箱；没有本地元数据的别名不会出现在这里。

### 15. 停用/激活/删除别名

```http
POST /api/aliases/:id/deactivate
POST /api/aliases/:id/reactivate
DELETE /api/aliases/:id
X-CSRF-Token: <token>

{"account_id": "acc_1"}
```

- `:id` 为别名的 `anonymousId`，非空且 URL 解码后不超过 256 字符
- `account_id` 必填
- `email` 可选：只在 `DELETE` 时有意义，传了会顺带清掉该别名的本地分组/备注
- 删除不可恢复；直接删除失败时会先停用再删

### 15.1. 批量操作别名

```http
POST /api/alias-bulk
X-CSRF-Token: <token>

{
  "account_id": "acc_1",
  "action": "deactivate",
  "group_id": "",
  "aliases": [
    {"anonymous_id": "anon_1", "email": "a@icloud.com"},
    {"anonymous_id": "anon_2", "email": "b@icloud.com"}
  ]
}
```

| 字段 | 说明 |
|---|---|
| `account_id` | 必填 |
| `action` | `deactivate` / `reactivate` / `delete` / `assign_group` |
| `aliases` | 1–200 条；`email` 在 `delete`（清理本地元数据）与 `assign_group` 时必填 |
| `group_id` | 仅 `assign_group` 使用，空串表示移出分组（备注保留） |

**响应：**

```json
{
  "success": true,
  "data": {
    "action": "deactivate",
    "total": 2,
    "applied": 1,
    "succeeded": 1,
    "failed": 1,
    "results": [
      {"anonymous_id": "anon_1", "email": "a@icloud.com", "ok": true},
      {"anonymous_id": "anon_2", "email": "b@icloud.com", "ok": false, "error": "上游返回 500"}
    ]
  }
}
```

- `results` 顺序与请求一致，便于前端按行回显。
- `deactivate` / `reactivate` / `delete` 逐条调用 iCloud（服务端最多 4 并发），单条失败不影响其余；`assign_group` 只改本地 `alias_meta.json`，整批一次落盘。
- 整批参数非法（缺 `account_id`、动作取值错误、超过 200 条、归组缺 `email`）返回 `400 VALIDATION_ERROR`；归入不存在的分组返回 `404 GROUP_NOT_FOUND`。
- 缺少 `anonymous_id` 的条目单独失败（`error` 说明原因），不会让整批报错。

### 16. 重新加载配置

```http
POST /api/reload
X-CSRF-Token: <token>
```

重新读取 `accounts.json`。

### 17. 运行信息

```http
GET /api/meta
```

**响应：**

```json
{
  "success": true,
  "data": {
    "version": "dev",
    "public_base_url": "https://mail.example.com"
  }
}
```

`public_base_url` 是分发链接的站点前缀：由设置页的「对外域名」或环境变量 `ICLOUD_HME_PUBLIC_BASE_URL` 决定，反代部署时用它拼出正确的取件链接。

---

## 别名池自动补号

后台按账号盯着**符合筛选条件的别名数量**，低于目标就自动创建补齐。配置存在数据目录的 `alias_pool.json`，与 `accounts.json` 解耦。

推荐的心智模型是**库存下限**：用 `filter.state=stock` + `filter.active=active`，让「可用库存」（还没发出去、且启用中的邮箱）始终不低于 `target`。分发出去一个库存就少一个，低于下限时自动补回 —— 发多少补多少，不会出现"突然发现没号可发"。

**统计范围（`filter`）决定「数哪些别名」：**

| 条件 | 取值 | 说明 |
|---|---|---|
| `filter.group_id` | 分组 ID 或空 | 只数这个分组里的邮箱；空 = 不限 |
| `filter.state` | `all` / `stock` | `stock` = 只数还没发出去的库存（推荐） |
| `filter.active` | `all` / `active` | `active` = 只数 iCloud 侧启用中的（推荐） |

三条约束是有意为之：

- **按分组统计时，新补的邮箱强制归入同一个分组** —— 否则补出来的号不计入筛选，会变成每轮都补；
- **不支持按「已分发 / 已回收 / 已停用」统计** —— 新补的号必然是库存且启用，这些条件永远补不满，同样会无限创建；"别把库存发完"用 `state=stock` 就已经覆盖；
- **不支持按 iCloud 标签统计** —— 标签是创建时写死的字段，自动补号不写标签，按它统计同样补不满。

**硬性约束（防止把上游打爆）：**

- 单轮最多创建 `max_per_run` 个，不会一次补几百个
- 两次创建之间强制间隔 3 秒
- 连续失败 3 次自动暂停，需重新保存配置解除
- 同一账号绝不并发检查

### 18. 查询补号配置

```http
GET /api/pool
```

**响应：** `{"configs": [PoolConfig]}`，未配置账号不出现在列表中。功能未启用（无数据目录）返回 `503 POOL_UNAVAILABLE`。

```json
{
  "account_id": "acc_3",
  "enabled": true,
  "target": 20,
  "max_per_run": 3,
  "interval_sec": 300,
  "group_id": "g_7f3a1c",
  "filter": {"group_id": "g_7f3a1c", "state": "stock", "active": "all"},
  "last_check_at": "2026-10-21T16:57:11+08:00",
  "last_created_at": "2026-10-21T16:57:11+08:00",
  "last_created": 3,
  "last_current": 17,
  "total_created": 17,
  "paused": false,
  "last_error": ""
}
```

### 19. 保存补号配置

```http
PUT /api/pool/:account_id
X-CSRF-Token: <token>
```

```json
{
  "enabled": true,
  "target": 20,
  "max_per_run": 3,
  "interval_sec": 300,
  "group_id": "g_7f3a1c",
  "filter": {
    "group_id": "g_7f3a1c",
    "state": "stock",
    "active": "all"
  }
}
```

| 字段 | 取值 | 说明 |
|---|---|---|
| `enabled` | bool | 是否启用 |
| `target` | 1–200 | 目标数量（针对筛选后的结果） |
| `max_per_run` | 1–10 | 单轮最多创建数，默认 3 |
| `interval_sec` | 60–86400 | 检查间隔，默认 300 |
| `group_id` | 分组 ID 或空 | 自动创建的别名归入哪个本地分组；空表示不分组 |
| `filter.group_id` | 分组 ID 或空 | 只统计这个分组；非空时 `group_id` 会被强制设成同一个值 |
| `filter.state` | `all` / `stock` | 只统计库存（还没发出去的） |
| `filter.active` | `all` / `active` | 只统计 iCloud 侧启用中的 |

`count_mode` 是旧字段，仍然接受（`active` 等价 `filter.active=active`），保存时会迁移并丢弃。

自动补号创建别名时**不传 `label`**：标签是 iCloud 侧字段，自动创建一律留空由 iCloud 补默认值，归类完全靠本地 `group_id`（分组只存在本系统，不会写到 iCloud）。

保存会清除自动暂停标记与失败计数（视为人工确认）。账号不存在、参数越界、筛选条件不安全（例如 `state=issued`）、筛选分组不存在，都返回 `400 VALIDATION_ERROR`。

### 20. 试算补号（只统计不创建）

```http
GET /api/pool/:account_id/preview?state=stock&active=active&group_id=&target=20
```

参数全部可选：

| 参数 | 说明 |
|---|---|
| `state` | `all` / `stock`；带了 `state` 就按参数试算，不带则用已保存配置 |
| `active` | `all` / `active` |
| `group_id` | 限定分组，空表示不限 |
| `target` | 目标数量，空或 0 表示用已保存的目标 |

用来在保存前回答「现在有几个、会补几个」。未配置过的账号也能试算（默认按 `stock` + `active`、目标 20），所以界面在新规则保存前就能预览效果。

**响应：**

```json
{
  "account_id": "acc_3",
  "current": 7,
  "target": 20,
  "to_create": 3,
  "breakdown": {"stock": 7, "issued": 18, "revoked": 2, "inactive": 1, "total": 27}
}
```

| 字段 | 说明 |
|---|---|
| `current` | 符合筛选条件的数量 |
| `to_create` | 本轮预计创建数（已按 `max_per_run` 截断；已达目标为 0） |
| `breakdown.stock` | 还没发出去的（含从未登记过元数据的） |
| `breakdown.issued` / `revoked` | 已分发 / 已回收 |
| `breakdown.inactive` | iCloud 侧已停用的（可能同时计入上面某一类） |
| `breakdown.total` | 账号下别名总数 |

**不创建任何别名。** 读取失败或筛选条件非法时不报 HTTP 错误，而是在 `error` 字段里返回原因，便于界面内联展示：

```json
{"account_id": "acc_3", "current": 0, "target": 5, "to_create": 0,
 "breakdown": {"stock": 0, "issued": 0, "revoked": 0, "inactive": 0, "total": 0},
 "error": "读取别名列表失败: 账号未配置 Cookie：请到「账号」页给这个账号更新 Cookie"}
```

### 21. 立即补齐一次

```http
POST /api/pool/:account_id/run
X-CSRF-Token: <token>
```

异步执行，立即返回 `{"triggered": true}`；补齐结果通过 `GET /api/pool` 的 `last_created` / `last_error` 观察。该账号正在检查时返回 `409 POOL_BUSY`。

### 22. 停止自动补号

```http
DELETE /api/pool/:account_id
X-CSRF-Token: <token>
```

删除配置并停止调度。未配置返回 `404 POOL_NOT_CONFIGURED`。

---

## 分组与别名元数据（本系统自有）

iCloud 只提供「邮箱 + 标签 + 是否启用 + 创建时间」四个字段，其中标签创建后不可修改，也没有分组的概念。

因此本系统额外维护一层**纯本地**的元数据（存在数据目录的 `alias_meta.json`）：

- 分组（`group`）：给邮箱归类，方便自己管理；删账号/删邮箱都不影响 Apple 侧设置
- 备注（`note`）：这个邮箱当初是干什么用的

「编辑邮箱」编辑的就是这两项 —— 邮箱地址由 iCloud 分配，标签由创建时写入，都不能改。

### 23. 列出分组

```http
GET /api/groups
```

```json
{
  "success": true,
  "data": {
    "groups": [
      {
        "id": "g_7f3a1c",
        "name": "购物",
        "color": "amber",
        "note": "只在购物站注册用",
        "revoke_after_days": 7,
        "created_at": "2026-10-01T10:00:00+08:00",
        "alias_count": 3
      }
    ],
    "count": 1
  }
}
```

`alias_count` 是本系统登记在该分组下的别名数量（不是 iCloud 全量统计）。

`revoke_after_days` 是该分组的**取件链接自动回收天数**：组内别名签发令牌 N 天后由后台自动撤销。缺省或 `0` 表示不自动回收。

### 24. 新建分组

```http
POST /api/groups
X-CSRF-Token: <token>

{"name": "购物", "color": "amber", "note": "只在购物站注册用", "revoke_after_days": 7}
```

- `name` 必填，去空格后 1–40 字符，忽略大小写判重
- `color` 可选，默认 `blue`；可选值 `blue` / `teal` / `green` / `amber` / `rose` / `purple` / `slate`
- `note` 可选，≤200 字符
- `revoke_after_days` 可选，`0–365`，默认 `0`（不自动回收）
- 分组上限 100 个

重名返回 `400 GROUP_NAME_TAKEN`，空名/超长返回 `400 VALIDATION_ERROR`。

### 25. 修改 / 删除分组

```http
PATCH /api/groups/:id
X-CSRF-Token: <token>

{"name": "购物网站", "color": "teal", "note": "", "revoke_after_days": 30}
```

仅传需要改的字段（局部更新）。不存在返回 `404 GROUP_NOT_FOUND`，天数越界（<0 或 >365）返回 `400 VALIDATION_ERROR`。

改 `revoke_after_days` 只影响**之后**的判定：已经在组内、且签发时间早已超过新天数的链接，会在下一轮后台回收（最多 10 分钟）时被撤销。想立刻生效就调一次 `POST /api/alias-token/sweep`。

```http
DELETE /api/groups/:id
X-CSRF-Token: <token>
```

```json
{"success": true, "data": {"deleted": true, "unassigned": 3}}
```

删除分组**不会**停用或删除任何邮箱，只把组内别名改回「未分组」，`unassigned` 为解绑数量。

### 26. 查询别名元数据

```http
GET /api/alias-meta[?account_id=acc_1]
```

```json
{
  "success": true,
  "data": {
    "entries": [
      {
        "email": "xyz123@icloud.com",
        "account_id": "acc_1",
        "group_id": "g_7f3a1c",
        "note": "注册试用",
        "state": "issued",
        "token": "xwsHnDAi2Kq9",
        "issued_at": "2026-10-21T20:55:05+08:00",
        "recipient": "客户甲",
        "last_received_at": "2026-10-21T19:02:00+08:00",
        "updated_at": "2026-10-21T20:55:05+08:00"
      }
    ],
    "count": 1
  }
}
```

`issued_at` 是当前这条取件令牌的签发时间（= 分发时间），自动回收按它起算；`recipient` 是"发给谁"的自由文本备注，只存在本地。

### 27. 写入 / 清除别名元数据

```http
PUT /api/alias-meta
X-CSRF-Token: <token>

{"email": "xyz123@icloud.com", "account_id": "acc_1", "group_id": "g_7f3a1c", "note": "注册试用"}
```

整体覆盖语义：`group_id` 与 `note` 都传空表示清除该别名的本地元数据。邮箱统一按小写存储，分组不存在返回 `400 GROUP_NOT_FOUND`。

可选字段 `state`（`stock` / `issued` / `revoked`）用来手工调整分发状态。语义上的硬约束：状态不是 `issued` 时令牌会被清空，避免出现"界面显示库存、链接却还能读"的不一致。

```http
DELETE /api/alias-meta?email=xyz123@icloud.com
X-CSRF-Token: <token>
```

### 28. 扫描收信活动

统计每个别名"最近一次收到邮件"的时间。别名都转发到同一个 iCloud 收件箱，因此只需扫一遍 INBOX：只取邮件的**信封**（不下载正文），用 `To` 头匹配别名列表。

```http
POST /api/alias-activity/scan
X-CSRF-Token: <token>

{"account_id": "acc_1", "days": 90, "limit": 500}
```

| 字段 | 取值 | 说明 |
|---|---|---|
| `account_id` | 必填 | 账号 ID |
| `days` | 1–365，默认 90 | 只扫最近 N 天的邮件 |
| `limit` | 1–2000，默认 500 | 最多拉取多少封信封 |

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "method": "imap",
    "scanned": 312,
    "matched": 47,
    "updated": 23,
    "window_days": 90,
    "last_scan_at": "2026-10-21T18:20:00+08:00"
  }
}
```

- `scanned` 本次扫描的邮件封数，`matched` 其中命中别名的封数，`updated` 收信时间被刷新的别名数。
- 只前进不后退：两次扫描乱序也不会把已有时间改小。
- 同一账号并发扫描返回 `409 ACTIVITY_BUSY`；账号既无 App 专用密码也无 Cookie 时返回 `400 VALIDATION_ERROR`。
- 扫描结果写入本地 `alias_meta.json` 的 `last_received_at`，随 `GET /api/aliases` 的 `meta` 一起返回。

### 29. 查询收信活动摘要

```http
GET /api/alias-activity?account_id=acc_1
```

```json
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "available": true,
    "activity": {"account_id": "acc_1", "last_scan_at": "2026-10-21T18:20:00+08:00", "scanned": 312, "matched": 47, "window_days": 90}
  }
}
```

`available` 为 `false` 表示该账号还没扫描过，`activity` 是零值。

---

## 标注（本系统自有）

iCloud 的**标签**（`label`）在创建别名时写入，之后不可修改，而且它属于 Apple 侧的数据。

但实际用起来最常变的信息恰恰是「这个邮箱干什么用、谁在用」。所以本系统另建了一套**可编辑、可复用**的标注（`tag`）：

- 标注是**全局目录**，一个标注可以挂到多个邮箱上
- 一个邮箱可以挂多个标注，互不覆盖
- 分三类：`purpose`（用途）、`owner`（使用者）、`other`（其它）
- 全部数据只存在 `alias_meta.json` 的 `tags` 字段与每条记录的 `tag_ids` 里，**不写 iCloud**

删除标注不会删除邮箱，只会把它从所有邮箱上摘掉。

### 30. 列出标注

```http
GET /api/tags
```

```json
{
  "success": true,
  "data": {
    "tags": [
      {
        "id": "t_9c1f0a2b3d4e",
        "name": "购物",
        "kind": "purpose",
        "color": "amber",
        "note": "电商注册用",
        "created_at": "2026-10-01T10:00:00+08:00",
        "alias_count": 3
      }
    ],
    "count": 1
  }
}
```

`alias_count` 是当前有多少个邮箱挂着这个标注。结果按 `kind`（用途 → 使用者 → 其它）、再按创建时间排序。

### 31. 新建标注

```http
POST /api/tags
```

```json
{
  "name": "客户甲",
  "kind": "owner",
  "color": "teal",
  "note": "外部试用客户"
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `name` | 是 | 最长 40 字符，全局不重复（忽略大小写） |
| `kind` | 否 | `purpose` / `owner` / `other`，留空按 `other` |
| `color` | 否 | `blue`/`teal`/`green`/`amber`/`rose`/`purple`/`slate` |
| `note` | 否 | 最长 120 字符 |

上限 200 个标注。重名返回 `400 TAG_NAME_TAKEN`。

### 32. 修改标注

```http
PATCH /api/tags/:id
```

只传要改的字段（`name` / `kind` / `color` / `note`），未传的保持不变。

### 33. 删除标注

```http
DELETE /api/tags/:id
```

```json
{"success": true, "data": {"deleted": true, "affected": 3}}
```

`affected` 是被摘掉该标注的邮箱数量。邮箱本身与 iCloud 数据都不受影响。

### 34. 设置某个邮箱的标注

```http
PUT /api/alias-tags
```

```json
{
  "email": "swift.otter.4821@icloud.com",
  "tag_ids": ["t_9c1f0a2b3d4e", "t_7a2b5c8d9e0f"]
}
```

**覆盖语义**：提交什么就是什么，传空数组 `[]` 表示全部摘掉。

返回该别名的完整元数据（含 `tag_ids`）。行为约定：

- ID 会去重，不存在的 ID 被**静默忽略**（标注被并发删除时不至于整批失败）
- 单个别名最多 20 个标注，超了返回 `400 VALIDATION_ERROR`

---

## 系统设置（运行时可改）

设置存在数据目录的 `settings.json`（权限 0600），改完**立即生效，不需要重启**。

与启动参数/环境变量的分工：

- 环境变量决定「进程怎么跑」——监听地址、数据目录、管理员口令，改了必须重启，页面上只做只读展示
- `settings.json` 决定「用起来之后才会想调的东西」——自动回收开不开、多久扫一次、取件页默认值、对外域名

### 35. 读取设置

```http
GET /api/settings
```

```json
{
  "success": true,
  "data": {
    "settings": {
      "auto_revoke_enabled": true,
      "auto_revoke_interval_min": 10,
      "public_mail_hours": 24,
      "public_mail_limit": 20,
      "public_base_url": ""
    },
    "env": {
      "data_dir": "E:\\icloud-hme\\data",
      "public_base_url_env": "",
      "version": "dev",
      "secure_cookie": false
    }
  }
}
```

`env` 是只读的部署信息，供设置页展示。

### 36. 保存设置

```http
PUT /api/settings
```

整表单覆盖，五个字段都要传：

| 字段 | 范围 | 说明 |
| --- | --- | --- |
| `auto_revoke_enabled` | `true`/`false` | 自动回收总开关。关掉后后台不再扫描，分组上的天数配置保留 |
| `auto_revoke_interval_min` | 1–1440 | 后台扫描间隔（分钟）。保存后后台协程按新周期重建 |
| `public_mail_hours` | 1–168 | 取件页 `/m` 的默认时间范围 |
| `public_mail_limit` | 1–50 | 取件页默认返回条数 |
| `public_base_url` | 空或 http(s) 地址 | 分发链接的站点前缀。**留空**表示按请求的 Host 推导 |

越界值返回 `400 VALIDATION_ERROR`，`message` 里带具体原因（如「扫描间隔只能是 1-1440 分钟」）。

`public_base_url` 的优先级：**设置页填的值 > 环境变量 `ICLOUD_HME_PUBLIC_BASE_URL` > 按请求 Host 推导**。设置页排在最前，是为了让「改完立刻生效」成立——环境变量改了要重启。

> 取件页的默认值只在链接**没带参数**时生效；链接里显式带 `?h=` / `?limit=` 时以参数为准。

---
## 对外取件接口（分发用）

这一节是给**收件人**用的，不是给管理员用的。

管理员把某个别名发出去时，交给对方的是一条带令牌的链接，而不是账号密码：

```
antics_drench.0h@icloud.com----https://your-host/m?f=html&e=antics_drench.0h@icloud.com:xwsHnDAi
```

- 冒号前是邮箱，冒号后是 12 位取件令牌；
- 令牌**只对应这一个别名**，只能读它的邮件，看不到账号、看不到其它别名，也改不了任何东西；
- 撤销令牌后链接立刻失效，邮箱本身不受影响。

### 37. 读取某个别名的邮件

```http
GET /m?f=html&e=<邮箱>:<令牌>
GET /m?f=json&e=<邮箱>:<令牌>
```

| 参数 | 说明 |
| --- | --- |
| `f` | `html`（默认，返回可直接打开的页面）或 `json`（给脚本对接）。其它值返回 400 |
| `e` | `邮箱:令牌`，必填 |
| `h` | 查询最近多少小时的邮件，默认 `24`，范围 `1-168`，越界按默认值处理 |
| `limit` | 最多返回多少封，默认 `20`，范围 `1-50` |

JSON 形态：

```json
{
  "code": 0,
  "data": {
    "recipient": "antics_drench.0h@icloud.com",
    "mail_query_hours": 24,
    "count": 1,
    "messages": [
      {
        "id": "12345",
        "from": "noreply@example.com",
        "to": "antics_drench.0h@icloud.com",
        "subject": "你的验证码",
        "date": "2026-10-21T12:00:00+08:00",
        "preview": "验证码 123456，5 分钟内有效",
        "body": "验证码 123456，5 分钟内有效"
      }
    ]
  }
}
```

HTML 形态是一个零脚本的单页：最近 24 小时没有邮件时每 20 秒自动刷新，拿到邮件后停下来，避免正在抄验证码时被刷掉。

错误响应（与 wdmail 那类取件服务保持一致的形状）：

| 情况 | HTTP | 响应体 |
| --- | --- | --- |
| 令牌不存在 / 邮箱与令牌不匹配 / 参数缺失 | `404` | `{"code":404,"data":{"message":"mailbox not found"}}` |
| **链接曾经有效但已失效**（被回收或被轮换） | `410` | HTML 为「这个链接已失效」页面；JSON 为 `{"code":410,"data":{"message":"link expired or revoked"}}` |
| `f` 不是 json/html | `400` | `{"code":400,"data":{"message":"f must be json or html"}}` |
| 触发限流 | `429` | `{"code":429,"data":{"message":"too many requests"}}` |
| 上游读取失败 | `502` | `{"code":502,"data":{"message":"upstream unavailable"}}` |

「从未存在」与「曾经有效但已失效」刻意区分开：

- 前者（令牌不存在）仍是统一的 `404`，不透露任何信息，避免被用来枚举别名；
- 后者返回 `410 Gone`，让对方明确知道是**链接过期**而不是地址打错了，页面上提示"请联系发件人重新发送一个链接"；
- 邮箱与令牌**仍然必须匹配**：即使令牌确实失效了，用错的邮箱去试同样只得到 `404`，否则就等于泄露了这个令牌原来属于谁。

失效判定靠的是本地记录：回收 / 轮换令牌时会把该令牌的 **SHA-256 指纹**存进 `alias_meta.json`（最多保留最近 8 个），不保存明文，文件泄露也无法还原出可用链接。

限流与缓存：同一令牌 / 同一 IP 各 60 次每分钟，全站 600 次每分钟；同一别名的取信结果缓存 10 秒。

### 38. 签发取件令牌

```http
POST /api/alias-token
X-CSRF-Token: <token>

{"account_id": "acc_1", "email": "xyz123@icloud.com", "recipient": "客户甲", "rotate": false}
```

```json
{
  "success": true,
  "data": {
    "email": "xyz123@icloud.com",
    "token": "xwsHnDAi2Kq9",
    "state": "issued",
    "issued_at": "2026-10-21T20:55:05+08:00",
    "url": "https://your-host/m?f=html&e=xyz123@icloud.com:xwsHnDAi2Kq9",
    "url_json": "https://your-host/m?f=json&e=xyz123@icloud.com:xwsHnDAi2Kq9",
    "distribute": "xyz123@icloud.com----https://your-host/m?f=html&e=xyz123@icloud.com:xwsHnDAi2Kq9",
    "public_base": "https://your-host"
  }
}
```

- **幂等**：已经有令牌且 `rotate=false` 时返回原令牌，不会因为多点一次就把发出去的链接弄失效；
- `rotate=true` 强制换新令牌，旧链接立即失效；
- `recipient` 可选，只记在本地，用来标记"这个邮箱发给了谁"；
- 签发会把别名状态置为 `issued`；
- `issued_at` 就是**分发时间**，自动回收按它起算。幂等复用不会刷新它（否则每点一次"复制链接"就等于给链接续命）；`rotate` 会刷新成新的当前时间。

### 39. 撤销取件令牌

```http
DELETE /api/alias-token?email=xyz123@icloud.com
X-CSRF-Token: <token>
```

清空令牌并把状态置为 `revoked`。别名与邮件都不受影响，之后可以重新签发。

批量形态走 `POST /api/alias-bulk`：`action` 取 `issue_token` 或 `revoke_token`。`issue_token` 会额外返回 `links`（邮箱 → 链接），前端据此导出分发清单。

### 40. 立即执行一次到期回收

```http
POST /api/alias-token/sweep
X-CSRF-Token: <token>
```

```json
{"success": true, "data": {"revoked": 2, "checked_at": "2026-10-21T21:40:16+08:00"}}
```

后台每 10 分钟自动跑一次，进程启动时也会先跑一轮（停机期间到期的链接不会漏掉）。这个接口用于"刚把分组的自动回收天数改小，想立刻生效"。

回收规则：

- 只看还持有令牌、且**已归入某个分组**的别名；未分组的永远不会被自动回收；
- 分组 `revoke_after_days` 为 `0` 表示不自动回收；
- 起算点是 `issued_at`（当前这条令牌的签发时间），不是别名创建时间；
- 判定到期后走的是和「撤销取件令牌」完全相同的路径：清空令牌、状态置 `revoked`，`issued_at` 保留供追溯。

---

## curl 使用示例（Cookie Jar + CSRF）

```bash
BASE="http://localhost:8081"

# 1. 登录,保存 Cookie 到 jar
curl -c cookies.txt -X POST "$BASE/api/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"password":"你的管理员密码"}'

# 2. 从响应中提取 csrf_token(可用 jq)
CSRF=$(curl -b cookies.txt "$BASE/api/auth/session" | jq -r '.data.csrf_token')

# 3. 读取账号列表(GET 无需 CSRF)
curl -b cookies.txt "$BASE/api/accounts"

# 4. 添加账号(mutation 需要 CSRF 头)
curl -b cookies.txt -X POST "$BASE/api/accounts" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"新账号","icloud_email":"owner@icloud.com"}'

# 5. 创建别名
curl -b cookies.txt -X POST "$BASE/api/create" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"account_id":"acc_1","label":"GitHub"}'

# 6. 读取邮件
curl -b cookies.txt "$BASE/api/inbox?account_id=acc_1&limit=10"
```

---

## 认证方式（iCloud 账号侧）

### Cookie 认证（功能最完整）

用于创建/停用/激活/删除别名、读取邮件（Web API 回退）。

**获取方式：**
1. 浏览器登录 [icloud.com](https://www.icloud.com) 或 [icloud.com.cn](https://www.icloud.com.cn) (国区)
2. F12 → Application → Cookies
3. 导出 Cookie 为 `{"key":"value"}` JSON，粘贴到管理界面「更新 Cookie」

**关键 Cookie：** `X-APPLE-WEBAUTH-TOKEN`（认证 token）、`X-APPLE-WEBAUTH-USER`（含 dsid）、`X-APPLE-WEBAUTH-HSA-TRUST`（设备信任）、`X-APPLE-DS-WEB-SESSION-TOKEN`（会话）

**有效期：** 约 24 小时

### App Password 认证（IMAP 优先读邮件）

用于 IMAP 读取邮件（优先路径，支持服务端按收件人搜索）。在 [appleid.apple.com](https://appleid.apple.com) → 登录和安全 → App 专用密码 生成。

---

## 技术说明

**Web API 路径** (`internal/mail/web_client.go`)：
1. 调用 `setup.icloud.com.cn/setup/ws/1/validate` 获取 `mccgateway` URL
2. 调用 `mccgateway/mailws2/v1/thread/search` 读取邮件

**⚠️ 已知坑：**
- `validate` 返回的 mccgateway URL 可能带 `:443` 端口，tls-client 的 cookie jar 按不带端口的 host 存储 cookie，带端口请求时 cookie 无法附加导致 403；**解决：** 解析 URL 后剥离端口号

**IMAP 路径** (`internal/mail/client.go`)：标准 IMAP 协议，连接 `imap.mail.me.com:993`，需要 App Password。

**升级差异（相对旧版）：**
- 全部 API 需要管理员登录（`401 AUTH_REQUIRED`）
- 账号响应不再返回 Cookie/密码/代理原文，改用 `has_cookies`/`has_app_password`/`has_proxy`
- `POST /api/accounts/:id/login` 成功响应不再返回 `cookies` 字段
- `accounts.json` 使用 `{"accounts": {id: {...}}}` map wrapper 格式（参考 `accounts.json.template`）

## 限制

- **创建频率**：iCloud 限制别名创建频率，过快会返回 429（服务端自动重试最多 5 次）
- **Cookie 有效期**：约 24 小时，需定期更新
- **邮件读取**：依赖 IMAP 连接，超时默认 30 秒
- **请求体上限**：1 MiB

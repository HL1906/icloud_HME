# iCloud Hide My Email 本地管理工具

[English](#english) | 中文

通过逆向 iCloud Web 接口和 IMAP 邮件协议，实现 Apple iCloud 隐藏邮箱别名的创建、列出和邮件收取功能。内置中文管理界面（React 单页应用，随二进制内嵌分发）。

## 功能特性

- ✅ **中文管理界面** — 浏览器访问 `http://localhost:8081` 即开即用
- ✅ **创建 HME 别名** — 自动生成 iCloud 隐藏邮箱地址
- ✅ **列出所有别名** — 查看账号下的所有 HME 别名
- ✅ **收取邮件** — 通过 IMAP 或 Web API 读取发到 HME 别名的邮件
- ✅ **双路径读信** — 邮件读取优先走 IMAP (App Password),无 App Password 时回退 Web API (Cookie)
- ✅ **多账号管理** — 支持多个 iCloud 账号并行管理
- ✅ **双认证模式** — Cookie (创建别名 + 读邮件回退) 和 App Password (IMAP 优先)
- ✅ **转发服务接口** — 按别名拉取「待转发」新邮件并登记已转发，避免重复转发
- ✅ **邮箱管理页** — 邮箱 / 标签 / 分组 / 启用状态 / 创建时间一表管理，支持搜索、筛选与排序
- ✅ **批量操作** — 勾选后批量归入分组 / 停用 / 激活 / 删除，逐条回显失败原因；一键导出当前筛选结果为 CSV
- ✅ **本地分组** — 分组和备注是本系统自己的归类方式，不写入 iCloud；可编辑归属，邮箱地址本身不可改
- ✅ **标注（第二套标签）** — iCloud 的标签建号后不能改，于是另建一套可编辑的标注，分「用途 / 使用者 / 其它」三类，可复用、可多选、可筛选；同样不写 iCloud
- ✅ **设置页** — 卡片式布局，按「库存 / 链接回收 / 取件页 / 域名」分区；自动回收开关与检查频率、取件页默认值、对外域名都能在页面上改，保存即生效（有改动才出现底部保存条）；自动补号按账号配置，全部账号一表列出、带库存水位进度条
- ✅ **最近收信时间** — 扫描收件箱（只取信封，不下载正文）统计每个邮箱最近一次收到邮件的时间，支持「从未收信 / 30 天 / 90 天未收信」筛选，顺手用批量停用清理僵尸号
- ✅ **对外取件链接（分发）** — 为每个别名签发一条 `邮箱:令牌` 链接，对方打开 `/m` 就能只看这一个邮箱的邮件，不用登录本站、也碰不到主号；库存 / 已分发 / 已回收三态分开管理，可批量签发、批量撤销、一键导出「邮箱----取件链接」分发清单
- ✅ **别名池自动补号** — 按「库存下限」补号：可用库存低于设定值就自动补齐，发多少补多少；可限定分组或改为数全部别名，保存前可试算，所有账号在设置页一览（自动创建不写标签，只按分组归档）
- ✅ **分发总览看板** — 隐藏邮箱总数、启用/停用、账号状态与最近创建一屏掌握
- ✅ **链接失效提示** — 对方拿到的是过期或被回收的链接时，看到的是「这个链接已失效，请联系发件人重新发送」，而不是含糊的 404；从不存在的令牌仍是 404，不泄露信息
- ✅ **安全模型** — 单管理员会话、CSRF 校验、登录限流、SSRF 防护、响应脱敏；建号按账号限流（每分钟 4 个）保护主号不被风控；`/api/healthz` 探活接口

## 快速开始

### 1. 安装

#### 方式一：下载二进制发布版（推荐）

从 [GitHub Releases](https://github.com/xiaozhou26/icloud-hme/releases) 下载对应平台的二进制文件：

| 平台 | 文件 |
|---|---|
| Linux x86_64 | `icloud-hme_linux_amd64` |
| Linux ARM64 | `icloud-hme_linux_arm64` |
| macOS Intel | `icloud-hme_darwin_amd64` |
| macOS Apple Silicon | `icloud-hme_darwin_arm64` |
| Windows x86_64 | `icloud-hme_windows_amd64.exe` |

```bash
# 示例：Linux 下直接运行（必须先设置管理员密码）
export ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026'
chmod +x icloud-hme_linux_amd64
./icloud-hme_linux_amd64
```

#### 方式二：Docker

```bash
# 拉取镜像
docker pull ghcr.io/xiaozhou26/icloud-hme:latest

# 运行（将本机 data 目录挂载进去）
docker run -d \
  --name icloud-hme \
  -p 8081:8081 \
  -v /path/to/data:/app/data \
  -e ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026' \
  ghcr.io/xiaozhou26/icloud-hme:latest
```

> ⚠️ 上面的密码仅为示例，**不可照抄**，请务必更换为至少 8 字符的强密码。

镜像支持 `linux/amd64` 和 `linux/arm64` 双架构，自动适配。

#### 方式三：源码编译（需要 Go 1.26+ 与 Node.js 22.12+ 双工具链）

```bash
# 前置要求: Go 1.26+、Node.js 22.12+
git clone https://github.com/xiaozhou26/icloud-hme.git
cd icloud-hme

# 一键构建（安装前端依赖 → 前端测试 → 前端构建 → Go 测试 → 编译）
./build.sh

# 或者手动分步构建
npm --prefix web ci
npm --prefix web run build
go build -o icloud-hme .
```

### 2. 安全配置（必读）

管理界面与 API 均需要管理员登录，升级后所有 API 都必须先通过 `POST /api/auth/login` 获取会话：

| 环境变量 | 说明 | 默认 |
|---|---|---|
| `ICLOUD_HME_ADMIN_PASSWORD` | 管理员初始密码，至少 8 字符 | 首次启动必填；已改过密码后可不设 |
| `ICLOUD_HME_SESSION_TTL` | 会话有效期 | `12h`（范围 `15m`–`168h`） |
| `ICLOUD_HME_SECURE_COOKIE` | 通过 TLS 反向代理部署时设为 `true` | `false` |

> **关于密码**：首次启动用环境变量初始化，密码经 argon2id 加盐派生后写入
> `data/admin_credential.json`（仅存哈希，权限 0600）。
> 之后可在界面左下角「修改密码」随时更换，**重启不会丢失**；
> 一旦改过密码，环境变量不再生效，以文件为准。
>
> **Breaking Change（v0.3+）**：升级后未设置 `ICLOUD_HME_ADMIN_PASSWORD` 将拒绝启动；
> 原有匿名 API 调用将收到 `401 AUTH_REQUIRED`。管理员会话只存内存，进程重启即失效。

### 3. 配置账号

在程序 `data/` 目录下创建 `accounts.json`（参考仓库内 `accounts.json.template`）：

```json
{
  "accounts": {
    "acc_1": {
      "id": "acc_1",
      "name": "主号",
      "real_email": "owner@example.com",
      "icloud_email": "owner@icloud.com",
      "cookies": {
        "X-APPLE-WEBAUTH-TOKEN": "token_value",
        "X-APPLE-WEBAUTH-USER": "v=1:s=1:d=22789132008"
      },
      "host": "icloud.com",
      "proxy": "http://user:pass@host:port",
      "app_password": "xxxx-xxxx-xxxx-xxxx",
      "status": "active"
    }
  }
}
```

> **提示:** 也可以通过管理界面的「账号」页面动态添加账号，无需手动编辑 JSON 文件。`cookies`、`app_password`、`proxy` 都是可选的。

### 4. 启动服务

```bash
# 二进制方式（默认 data 目录）
export ICLOUD_HME_ADMIN_PASSWORD='your-strong-password'
./icloud-hme_linux_amd64

# 指定端口和数据目录
./icloud-hme_linux_amd64 -addr :9090 -data ./my_data

# 调试模式（启用请求日志）
./icloud-hme_linux_amd64 -debug

# 查看完整参数
./icloud-hme_linux_amd64 -h
```

服务默认监听 `:8081`。浏览器打开 `http://localhost:8081` 进入管理界面（账号 / 别名 / 收件箱）。完整 API 契约见 [API.md](API.md)。

> **对外分发（反代部署必看）：** 取件链接里的站点前缀默认按请求的 `Host` 推导。挂在反向代理后面、或用自定义域名对外时，请显式指定，否则生成出来的链接会指向内网地址：
>
> ```bash
> ICLOUD_HME_PUBLIC_BASE_URL='https://mail.example.com' ./icloud-hme_linux_amd64
> ```

## 分发隐藏邮箱（对外取件接口）

这一节解决的是最实际的场景：**把隐藏邮箱发给别人用，但不想让对方登录本站、更不想让他摸到主号**。

### 工作方式

每个别名可以签发一条取件令牌，组成链接：

```
https://your-host/m?f=html&e=antics_drench.0h@icloud.com:xwsHnDAi2Kq9
```

- 收件人打开这条链接，只能看到**这一个别名**最近 24 小时的邮件（可调 1-168 小时），零登录；
- 令牌只读、只对应一个别名，看不到账号、看不到其它别名，也改不了任何东西；
- 撤销后链接立刻失效，邮箱本身不受影响，随时可以重新签发。

### 三态管理

邮箱管理页多了一列「分发」，把建好的和发出去的分开：

| 状态 | 含义 | 能做什么 |
| --- | --- | --- |
| 库存 `stock` | 建好还没发出去 | 生成取件链接 |
| 已分发 `issued` | 已签发令牌，链接可被外人读取 | 复制链接 / 撤销 |
| 已回收 `revoked` | 发出去的链接已撤销 | 重新生成 |

配合筛选框可以一眼看出"还有多少库存没发""发出去多少"，以及批量签发、批量撤销。

### 分发清单

工具栏的「分发清单」按当前筛选结果导出，每行一条，直接就是可分发格式：

```
antics_drench.0h@icloud.com----https://your-host/m?f=html&e=antics_drench.0h@icloud.com:xwsHnDAi2Kq9
```

已经签发过的复用原链接（不会让发出去的失效），库存里的顺带签发。

### 分发时间与到期自动回收

每次签发都会记下**分发时间**（`issued_at`），列表的「分发」列只显示状态徽章，鼠标悬停看精确时间（如 `2026/10/21 22:15`）；「自动回收时间」单独一列，直接给出到期时刻。

分发时间有两个用途，一个是给人看的，一个是给规则用的：

**按分组设定生命周期。** 同一批发出去的号往往有一致的期限——试用 7 天、月付 30 天。所以在分组上设一个「自动回收」天数：

| 分组设置 | 效果 |
| --- | --- |
| 不自动回收（默认） | 链接一直有效，直到你手动撤销 |
| 签发 N 天后 | 组内别名签发满 N 天，后台自动撤销令牌 |

后台每 10 分钟扫一次（间隔可在「设置」页改），进程启动时也会先跑一轮（停机期间到期的不会漏）。列表的「自动回收时间」列给出具体到期时刻：剩 2 天以内标黄，已过期标红，下一轮就会被收掉。

三条边界是刻意这样设计的：

- **未分组的邮箱永远不自动回收** —— 一次误设不会波及全部库存；
- **天数 0 = 不回收** —— 默认就是安全的，必须显式打开；
- **起算点是分发时间，不是创建时间** —— 一个号可能建了三个月才发出去，寿命从发出去那天算。

回收只是让链接失效，邮箱本身还在，重新签发一条就能继续用。另外，**幂等复用不会刷新分发时间**：你反复点「复制链接」不会给链接续命，否则自动回收永远不生效；只有主动「换新令牌」才会重置计时。

### 和 iCloud+ 的关系

隐藏邮箱是 **iCloud+ 订阅的一部分**，这一点绕不开：

- 订阅有效时，别名正常收信，本系统才能读到邮件；
- **订阅到期后，Apple 那边不再往这些别名投递邮件**，别名记录通常还在账号里，重新订阅一般能恢复（具体以 Apple 的政策为准）；
- 你的**本地数据完全不受影响**：分组、备注、分发时间、令牌都还在 `alias_meta.json` 里。

所以订阅到期时会发生一件容易踩的事：取件链接**不会变成 404**（令牌还在），而是打开后提示「读取邮件失败」——因为上游读不到了。对外看就是"页面打不开了"。

如果不想让它以这种方式暴露，订阅到期前把发出去的链接批量撤销一遍，取件页会干净地回 `404 mailbox not found`，对方看到的是"这个邮箱已失效"，而不是一个报错页面。

### 给脚本对接

把 `f=html` 换成 `f=json` 即可拿到结构化数据，形状与 wdmail 那类取件服务一致：

```bash
curl "https://your-host/m?f=json&e=antics_drench.0h@icloud.com:xwsHnDAi2Kq9"
# {"code":0,"data":{"recipient":"...","mail_query_hours":24,"count":1,"messages":[...]}}
```

令牌错误一律返回同一个 `404 mailbox not found`，不区分"邮箱错"还是"令牌错"；同一令牌与同一 IP 各限 60 次/分钟，取信结果缓存 10 秒。完整契约见 [API.md](API.md) 的「对外取件接口」一节。

## 标注（第二套标签）

iCloud 的「标签」在创建邮箱时写入，之后**不可修改**——它是 Apple 侧的字段，本系统只能读。

而实际最常变的信息恰恰是「这个邮箱干什么用、谁在用」。所以另建了一套**标注**：

- 在「标注」页维护目录，分**用途 / 使用者 / 其它**三类；
- 一个标注可以挂到多个邮箱上，一个邮箱也可以挂多个标注；
- 在「邮箱管理」里点「编辑」即可分配；列表多一列「标注」，点标注可直接筛出同类邮箱；
- 删除标注只会把它从邮箱上摘掉，邮箱本身和 iCloud 数据都不受影响。

标注只存在 `alias_meta.json`，**不写 iCloud**，也不影响 iCloud 上那个改不了的标签。

## 设置

「设置」页放的是**改完立即生效、不需要重启**的东西：

| 设置 | 作用 |
| --- | --- |
| 启用自动回收 | 总开关。关掉后后台不再扫描，分组上的天数配置保留 |
| 检查间隔 | 后台多久扫一次到期链接（1-1440 分钟，默认 10） |
| 取件页默认时间范围 / 条数 | 别人打开 `/m` 时默认看多少（链接带 `?h=` / `?limit=` 时以参数为准） |
| 对外域名 | 分发链接的站点前缀。留空按访问地址推导，挂在反代后面时必须填 |

设置存在数据目录的 `settings.json`（权限 0600），重启后仍在。监听地址、数据目录、管理员口令属于**部署期配置**，改了要重启，所以页面上只做只读展示。

### 自动补号

同一页还有补号总览表：**所有账号都会列出来**，没配过的显示「未开启」，点「开启补号」就能配，不用再去账号页翻。

一条补号规则就两件事：

| | 说明 |
| --- | --- |
| **库存下限** | 可用库存（还没发出去、且启用中的）少于多少个就要补 |
| **统计范围** | 数哪些邮箱：默认只算库存 + 仅启用中，可再限定分组；也可改成数全部别名 |

比如「让『试用客户』这组的**库存**始终有 20 个」：统计范围选该分组 + 仅库存，下限填 20。后台每 N 分钟数一次，不够就补到 20，新补的邮箱自动进这个分组。**分发出去一个库存就少一个，低于下限自动补回来**，所以不用担心发到一半没号了。

弹窗里保存前会先**试算**：显示当前可用库存、本轮会补几个，以及账号下「库存 / 已分发 / 已回收 / 停用」各有多少，方便判断还剩多少可发。试算只统计、不创建（`GET /api/pool/:account_id/preview`）。

三条约束是为了不让它变成"无限补号"：

- 按分组统计时，新补的邮箱**强制进同一个分组**——否则补出来的号不算数，会一轮接一轮地补；
- 不能按「已分发 / 已回收 / 已停用」统计——新号一定是库存且启用，这些条件永远补不满；想「别把库存发完」用"只算库存"就已经覆盖；
- 不能按 iCloud 标签统计——标签建号时写死，自动补号不写标签，同理补不满。

表格里的「库存水位」是上次检查时的结果（进度条 + 当前 / 下限），旁边的「补一次」可以立刻触发一轮，不用等间隔。

---

## API 接口

> **认证**：除 `POST /api/auth/login` 与 `GET /api/auth/session` 外，所有 `/api` 接口都需要管理员会话 Cookie（`hme_session`）；非 GET/HEAD/OPTIONS 请求还需携带 `X-CSRF-Token` 请求头。完整契约与 curl 示例见 [API.md](API.md)。

### 核心接口

#### 创建 HME 别名

```bash
POST /api/create

# 请求体
{
  "account_id": "acc_1",      # 必填: 账号 ID
  "label": "注册某网站"        # 可选: 别名标签
}

# 响应
{
  "success": true,
  "data": {
    "email": "xyz123@icloud.com",
    "label": "注册某网站",
    "created_at": "2024-01-15T10:30:00Z",
    "account_id": "acc_1"
  }
}
```

#### 读取邮件

```bash
GET /api/inbox?account_id=acc_1&alias=xyz123@icloud.com&limit=20&days=7

# 参数说明:
#   account_id - 必填: 账号 ID
#   alias      - 可选: 只读取发到该别名的邮件
#   limit      - 可选: 返回邮件数量 (默认 20)
#   days       - 可选: 查找最近几天的邮件 (默认 7,仅 IMAP 模式)

# 响应
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
        "from": "noreply@example.com",
        "to": "xyz123@icloud.com",
        "subject": "欢迎注册",
        "preview": "感谢您的注册...",
        "date": "2026-07-09T14:32:10+08:00"
      }
    ]
  }
}

# 读取方式 (自动选择):
#   method: "imap"    — 通过 App Password 认证 (优先)
#   method: "web_api" — 通过 Cookie 认证,无需 App Password (回退)
```

#### 转发服务接口（按别名取待转发邮件）

面向「隐藏邮箱分发给多人、再由服务按别名转发」的场景：

```bash
# 1. 只拉取尚未转发的邮件(alias 必填)
GET /api/forward?account_id=acc_1&alias=xyz123@icloud.com&limit=20&days=7&include_body=1

# 响应
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "alias": "xyz123@icloud.com",
    "count": 1,
    "method": "imap",
    "messages": [
      {"id": "1042", "subject": "欢迎注册", "forwarded": false, "body": "…"}
    ]
  }
}

# 2. 转发成功后登记,下次轮询不再返回这批邮件
POST /api/forward/ack
{"account_id": "acc_1", "alias": "xyz123@icloud.com", "ids": ["1042"]}

# 响应
{"success": true, "data": {"account_id": "acc_1", "alias": "xyz123@icloud.com", "updated": 1, "forwarded": true}}
```

`forwarded: false` 可取消标记（转发失败需重试时用）。标记持久化在数据目录的 `forwarded.json`，
按「账号 + 别名 + 邮件 ID」去重，别名大小写不敏感。

### 账号管理接口

#### 列出所有账号

```bash
GET /api/accounts

# 响应
{
  "success": true,
  "data": [
    {"id": "acc_1", "name": "主号"},
    {"id": "acc_2", "name": "副号"}
  ]
}
```

#### 添加账号

**简化版（cookies 可选）:**

```bash
POST /api/accounts

# 请求体
{
  "name": "新账号",
  "host": "icloud.com",           # 可选
  "proxy": "http://..."           # 可选
}

# 响应 - 状态为 pending,需登录
{
  "success": true,
  "data": {
    "id": "acc_xxx",
    "name": "新账号",
    "status": "pending"
  }
}
```

**完整版（带 Cookie）:**

```bash
POST /api/accounts

# 请求体
{
  "name": "新账号",
  "cookies": "{\"x-apple-session-token\":\"token_value\"}",  # JSON 或 Header 格式
  "host": "icloud.com",           # 可选
  "proxy": "http://..."           # 可选
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_3",
    "name": "新账号",
    "status": "active"
  }
}
```

#### 账号登录（获取 Cookie）

```bash
POST /api/accounts/:id/login

# 请求体
{
  "password": "用户的常规iCloud密码",  # 不是 App Password
  "otp_code": "123456"                  # 可选,2FA 验证码
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_1",
    "cookies": {
      "x-apple-session-token": "...",
      "X-APPLE-WEBAUTH-TOKEN": "..."
    }
  }
}
```

#### 删除账号

```bash
DELETE /api/accounts/:id

# 响应
{
  "success": true,
  "data": {"id": "acc_3"}
}
```

#### 设置 App Password

```bash
POST /api/accounts/:id/password

# 请求体
{
  "icloud_email": "your_email@icloud.com",
  "app_password": "xxxx-xxxx-xxxx-xxxx"
}

# 响应
{
  "success": true,
  "data": {
    "id": "acc_1",
    "icloud_email": "your_email@icloud.com"
  }
}
```

### 别名管理接口

#### 列出所有别名

```bash
GET /api/aliases?account_id=acc_1

# 响应
{
  "success": true,
  "data": {
    "account_id": "acc_1",
    "count": 15,
    "aliases": [
      {
        "email": "xyz123@icloud.com",
        "label": "注册某网站",
        "created_at": "2024-01-15T10:30:00Z"
      }
    ]
  }
}
```

#### 停用别名

```bash
POST /api/aliases/:id/deactivate

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123",
    "success": true
  }
}
```

#### 激活别名

```bash
POST /api/aliases/:id/reactivate

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123",
    "success": true
  }
}
```

#### 删除别名

```bash
DELETE /api/aliases/:id

# 请求体
{
  "account_id": "acc_1"
}

# 响应
{
  "success": true,
  "data": {
    "anonymous_id": "abc123"
  }
}
```

## 认证方式

### 方式一: Cookie 认证 (推荐,功能最完整)

Cookie 认证可实现所有功能:创建别名、读取邮件、管理别名。

**适用范围:**
- 创建/停用/激活/删除 HME 别名 ✅
- 读取邮件 (通过 iCloud Web API,无需 App Password) ✅

**获取 Cookie:**

1. 使用浏览器登录 [icloud.com](https://www.icloud.com) 或 [icloud.com.cn](https://www.icloud.com.cn) (国区)
2. 打开浏览器开发者工具 (F12)
3. 进入 Application → Cookies
4. 导出全部 Cookie 为 `{"key":"value"}` 格式的 JSON

**关键 Cookie (必需):**
- `X-APPLE-WEBAUTH-TOKEN` — 认证 token
- `X-APPLE-WEBAUTH-USER` — 含 dsid (`v=1:s=1:d=22789132008`)
- `X-APPLE-WEBAUTH-HSA-TRUST` — 设备信任 token
- `X-APPLE-DS-WEB-SESSION-TOKEN` — 会话 token

**注意:** 导出的 Cookie 值不要包含多余的引号或转义字符。

### 方式二: App Password 认证 (IMAP,优先读邮件)

App Password 用于 IMAP 读取邮件,是邮件读取的优先路径 (支持服务端按收件人搜索)。

**生成 App Password:**

1. 登录 [appleid.apple.com](https://appleid.apple.com)
2. 进入 "登录和安全" → "App 专用密码"
3. 生成新密码,用于此工具

### 邮件读取双路径

`GET /api/inbox` 自动选择读取方式:

1. **优先: IMAP (App Password)** — 设置了 App Password 时使用,支持服务端按收件人 (`TO`) 搜索
2. **回退: Web API (Cookie)** — 无 App Password 或 IMAP 失败时,通过 `mccgateway` 端点读取,本地按别名过滤

响应中包含 `"method": "web_api"` 或 `"method": "imap"` 字段,标识实际使用的读取方式。

## 安全说明

### 已实施的防护

| 风险 | 防护措施 |
|---|---|
| **SSRF**（自定义收件邮箱可指向内网） | 拨号层地址校验（`internal/netsafe`）：拒绝回环/私有/CGNAT/链路本地/云元数据地址，端口白名单 993/143/1993/2993；DNS 解析后的每个 IP 都校验 |
| **登录爆破 / 全站锁定** | 按 IP 限流 + 全局失败上限；**密码正确时不受限流影响**，避免有人刷错密码把管理员一起锁在门外 |
| **CSRF** | 服务端存储的 per-session token（常量时间比较）+ 请求头；另校验 `Origin` 与 Host 同源 |
| **凭据泄露到日志** | verbose 模式不再打印 Cookie 值与请求头值；URL 只打印到 `?` 前 |
| **会话安全** | CSPRNG 32 字节 token、内存只存 SHA-256、`HttpOnly` + `SameSite=Strict`、TTL 过期即清理、上限防内存增长 |
| **口令存储** | argon2id(64 MiB) + 随机 salt，登录用常量时间比较；密码长度上限 1024 字节 |
| **超大请求** | 请求体上限 1 MiB；`ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`MaxHeaderBytes` 齐全，抗 Slowloris |
| **越权取邮件** | 转发接口校验 alias 必须属于该账号，防止按别人的别名取 OTP |
| **响应头** | CSP（`object-src 'none'`、`frame-ancestors 'none'`）、`X-Frame-Options: DENY`、`nosniff`、`Referrer-Policy`、COOP/CORP；TLS 部署时自动加 HSTS |
| **数据落盘** | `accounts.json` / `forwarded.json` 原子写（临时文件 + fsync + rename），权限 0600，数据目录 0700 |
| **日志隐私** | 访问日志不记录 query string（避免把别名写进日志） |
| **邮件内容渲染** | 后端剥离 HTML/CSS，前端以文本节点渲染；无 `dangerouslySetInnerHTML` |
| **取件链接被枚举** | 令牌 12 位 base62（约 71 bit 熵）；查表用常量时间比较且不提前返回；任何失败都回同一个 404，不区分邮箱错与令牌错 |
| **取件接口被刷** | 按令牌 + 按 IP 各 60 次/分钟、全站 600 次/分钟；同一别名取信缓存 10 秒，避免把上游 IMAP 打爆 |
| **取件页被收录 / 被嵌站** | `/m` 单独下发更严的 CSP（`default-src 'none'`、零脚本）、`X-Robots-Tag: noindex`、`Referrer-Policy: no-referrer`、`frame-ancestors 'none'` |
| **令牌泄漏影响面** | 一条令牌只能读一个别名的邮件；撤销即时生效；管理端可随时回收 |

### 部署注意

- 生产环境请放在 TLS 反向代理后，并设置 `ICLOUD_HME_SECURE_COOKIE=true`（启用 Secure Cookie 与 HSTS）。
- 管理员密码建议 16 字符以上随机串；会话只存内存，重启即失效。
- 受限于「单管理员」模型，请勿直接把 8081 暴露到公网。
- **只有 `/m` 需要对外**：如果要用分发功能，建议在反向代理上只放行 `/m`，`/api/*` 与管理界面继续留在内网或加额外认证。用 `ICLOUD_HME_PUBLIC_BASE_URL` 指定对外域名。
- **本地自建 IMAP 测试**：SSRF 防护默认禁止内网地址。若确实需要连接内网的收件邮箱服务器，需在代码中显式放开（`netsafe.Policy{AllowPrivate: true}`）。

## 项目架构

```
icloud-hme/
├── main.go                 # 入口: 读取安全配置、加载账号、启动服务
├── web/                    # 前端工程 (React + TypeScript + Vite)
│   └── src/                #   管理界面源码
├── accounts.json           # 账号配置文件 (自动生成)
├── go.mod
└── internal/
    ├── account/
    │   ├── manager.go      # 多账号管理器 (持久化、客户端工厂)
    │   └── public.go       # 公开 DTO (Summary) 与输入校验
    ├── auth/
    │   ├── manager.go      # 管理员会话 + CSRF
    │   └── limiter.go      # 登录失败限流
    ├── hme/
    │   ├── client.go       # iCloud HME Web 客户端 (Cookie 认证)
    │   └── auth.go         # SRP 登录 (账号密码 + 2FA 获取 Cookie)
    ├── mail/
    │   ├── client.go       # IMAP 邮件客户端 (App Password 认证)
    │   └── web_client.go   # Web 邮件客户端 (Cookie 认证,无需 App Password)
    ├── server/
    │   ├── server.go       # 路由分组 (认证 + CSRF)
    │   ├── backend.go      # 业务接口与 Manager 适配器
    │   ├── auth.go         # 登录/会话/退出 handler 与中间件
    │   ├── account_handlers.go  # 账号管理 handler
    │   └── middleware.go   # 安全响应头、请求上限
    └── webui/
        └── embed.go        # 内嵌前端资源 + SPA fallback
```

### 核心模块

- **account.Manager**: 管理多个 iCloud 账号,负责配置持久化和客户端创建
- **hme.Client**: 封装 iCloud HME Web API,支持 Cookie 认证
- **hme.auth**: SRP 协议登录,支持账号密码 + 可选 2FA
- **mail.Client**: IMAP 邮件客户端 (App Password,优先读邮件)
- **mail.WebClient**: 通过 iCloud Web API (mccgateway) 读取邮件,无需 App Password
- **server.Server**: HTTP API 服务 + 管理界面静态资源

## 技术栈

- **Go 1.26+** / **Gin** — HTTP 框架
- **React 19 + TypeScript + Vite 8** — 管理界面
- **go-imap** — IMAP 协议实现
- **tls-client** — TLS 指纹模拟 (绕过 iCloud 反爬)

## 常见问题

### Q: 创建别名返回 401/403 错误?

**A:** Cookie 已过期，需要重新获取。iCloud Cookie 有效期通常为 24 小时。

### Q: 读取邮件返回超时?

**A:** 检查网络连接，确保可以访问 `imap.mail.me.com:993`。

### Q: 如何查看某个别名收到了哪些邮件?

**A:** 调用 `GET /api/inbox?account_id=acc_1&alias=your_alias@icloud.com`

### Q: 支持同时管理多个 iCloud 账号吗?

**A:** 支持，在 `accounts.json` 中配置多个账号即可，每个账号有独立的 `id`。

## 开发指南

### 本地开发

```bash
# 前端开发模式 (vite dev server, /api 代理到 :8081)
npm --prefix web ci
npm --prefix web run dev

# 后端开发模式
export ICLOUD_HME_ADMIN_PASSWORD='your-strong-password'
go run main.go -debug

# 前端检查 (lint + test + build)
npm --prefix web run check

# 完整构建 (含前端)
./build.sh

# 交叉编译
GOOS=linux GOARCH=amd64 go build -o icloud-hme .
GOOS=windows GOARCH=amd64 go build -o icloud-hme.exe .
```

### 发布

推送 `v*` tag 到 GitHub 自动触发 CI：

```bash
git tag v0.2.0 && git push origin --tags
```

Actions 会自动构建多平台二进制、Docker 镜像（`ghcr.io/xiaozhou26/icloud-hme`）并创建 Release。

### 代码规范

- 代码注释使用中文
- 错误信息返回给用户时使用中文
- API 响应格式统一: `{success: bool, data: any, message: string}`

## 许可证

MIT License

---
## 社区

友情链接：[LINUX DO](https://linux.do)

## English

A local management tool for Apple iCloud Hide My Email (HME) aliases, supporting creation, listing, and email reading through reverse-engineered iCloud Web API and IMAP protocol. Ships with a built-in Chinese management UI (React SPA embedded in the single binary).

### Features

- Built-in management UI at `http://localhost:8081`
- Create HME aliases automatically
- List all aliases for an account
- Read emails sent to HME aliases via IMAP or Web API
- Manage multiple iCloud accounts
- Dual authentication: Cookie and App Password
- Security: single-admin session, CSRF checks, login rate limiting, redacted API responses

### Quick Start

#### Option 1: Binary (GitHub Releases)

Download the latest binary from [GitHub Releases](https://github.com/xiaozhou26/icloud-hme/releases):

| Platform | File |
|---|---|
| Linux x86_64 | `icloud-hme_linux_amd64` |
| Linux ARM64 | `icloud-hme_linux_arm64` |
| macOS Intel | `icloud-hme_darwin_amd64` |
| macOS Apple Silicon | `icloud-hme_darwin_arm64` |
| Windows x86_64 | `icloud-hme_windows_amd64.exe` |

```bash
# Linux example (admin password is REQUIRED, min 8 chars)
export ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026'
chmod +x icloud-hme_linux_amd64
./icloud-hme_linux_amd64
```

#### Option 2: Docker

```bash
docker pull ghcr.io/xiaozhou26/icloud-hme:latest

docker run -d \
  --name icloud-hme \
  -p 8081:8081 \
  -v /path/to/data:/app/data \
  -e ICLOUD_HME_ADMIN_PASSWORD='change-this-before-running-2026' \
  ghcr.io/xiaozhou26/icloud-hme:latest
```

> The password above is only an example — do NOT copy it. Use a strong password with at least 8 characters.

#### Option 3: Build from source (Go 1.26+ and Node.js 22.12+)

```bash
git clone https://github.com/xiaozhou26/icloud-hme.git
cd icloud-hme

# One-shot build (frontend deps → frontend test → frontend build → Go test → binary)
./build.sh

# Or step by step
npm --prefix web ci
npm --prefix web run build
go build -o icloud-hme .
```

### Configuration

| Env var | Description | Default |
|---|---|---|
| `ICLOUD_HME_ADMIN_PASSWORD` | Admin password, **required**, min 8 chars | none (refuses to start) |
| `ICLOUD_HME_SESSION_TTL` | Session TTL | `12h` (range `15m`–`168h`) |
| `ICLOUD_HME_SECURE_COOKIE` | Set `true` when deployed behind TLS | `false` |

> **Breaking change (v0.3+)**: without `ICLOUD_HME_ADMIN_PASSWORD` the server refuses to start; all API endpoints now require login (`401 AUTH_REQUIRED`). Admin sessions are in-memory only and are lost on restart.

Create `data/accounts.json` (see `accounts.json.template`) and start the server (default port `:8081`). Open `http://localhost:8081` to use the management UI. Full API contract: [API.md](API.md).

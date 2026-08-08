# chatgpt-register

> **Grok（x.ai）账号全自动批量注册管理台** · python + nodriver 无头注册 · 自动提取 sso cookie

---

🌐 **生图站** [vividai.run](https://vividai.run) &nbsp;|&nbsp;
👥 **QQ 交流群** [1106849765](https://qm.qq.com/q/1106849765) &nbsp;|&nbsp;
🐧 **QQ** 1114639355 &nbsp;|&nbsp;
🛒 **小店** [pay.ldxp.cn/shop/chiyi](https://pay.ldxp.cn/shop/chiyi) &nbsp;|&nbsp;
✉️ **邮箱** [vividairun@gmail.com](mailto:vividairun@gmail.com)

---

## ✨ 核心优势

| 🚀 30 秒极速注册 | ✅ 百分百成功率 | 📬 邮箱自动收码 |
|:---:|:---:|:---:|
| python + nodriver 无头自动化，全程无需人工干预 | 验证码自动从邮箱读取，全流程零手动操作 | 每个邮箱注册 1 个账号，用邮箱本身地址 |

| 🌐 代理池轮转 | 📊 可视化管理台 | 📦 零依赖部署 |
|:---:|:---:|:---:|
| 内置代理池按账号轮转，多 IP 并发注册不封号 | 毛玻璃风格 UI，实时仪表盘 + 执行日志可视化 | 纯 Go 编译单文件，无需安装任何环境，下载即用 |

---

## 🤖 无头注册——技术亮点

> 注册流程由 **python + nodriver** 驱动真实 Chromium/Edge 内核无头完成（Go 端 subprocess 调用 `pyreg/`），
> 拿到 `sso` cookie 即完成，入库后可批量导出（一行一个）。

### 注册全流程（全自动，无需人工）

```
Go 调 pyreg/grok_register.py（无头启动浏览器）
    ↓
打开 accounts.x.ai 注册页，用邮箱方式注册
    ↓
自动填写邮箱 + 随机密码 + 随机姓名
    ↓
python 打印 __NEED_CODE__ → Go 从 vary.email / 邮箱读取验证码（C1O-6KS 去连字符）写回其 stdin
    ↓
等待 Cloudflare Turnstile token 就绪后提交注册
    ↓
轮询 grok.com 的 cookie 提取 sso（最多 120 秒）
    ↓
Go 组装 auth 数据写入数据库，账号状态更新为「已注册」
```

### 关键技术点

| 特性 | 说明 |
|------|------|
| **python + nodriver** | 注册不在 Go 里复现，直接 subprocess 调 `pyreg/grok_register.py`，天然无头、无 webdriver 特征 |
| **验证码自动读取** | 对接 vary.email 取件 / Outlook 邮箱 API，每 5 秒轮询一次，无需人工复制粘贴 |
| **Turnstile 等待** | 只等页面自己拿到 `cf-turnstile-response`，拿不到就不提交，不做破解 |
| **sso cookie 提取** | 注册提交后轮询 cookie，拿到 `sso` / `sso-rw` 即入库，不再换 token |
| **IP 与浏览器一致** | 注册全程走同一个代理出口，避免风控拦截 |
| **无头模式** | 默认无头，无需显示器，支持服务器 / VPS 部署 |
| **并发安全** | 多个注册任务并发执行，每个任务一个独立 python 进程与浏览器实例，互不干扰 |

### 运行前置

Go 端只负责调度，注册脚本需要本机的 python 环境：

```bash
pip install nodriver curl_cffi
```

| 环境变量 | 说明 |
|----------|------|
| `GROK_PYTHON` | python 解释器，默认 Windows `python` / 其它 `python3` |
| `GROK_PYREG_DIR` | 脚本目录，默认工作目录下的 `pyreg` |
| `EDGE_PATH` | 浏览器可执行路径，默认本机 Edge |

---

## 截图预览

| 仪表盘 | 账户管理 |
|:---:|:---:|
| ![仪表盘](./screenshots/dashboard.png) | ![账户管理](./screenshots/accounts.png) |

| 执行日志 | 邮箱管理 |
|:---:|:---:|
| ![执行日志](./screenshots/accounts-log.png) | ![邮箱管理](./screenshots/mailboxes.png) |

| 邮件取件（自动读取验证码） |
|:---:|
| ![邮件取件](./screenshots/mailboxes-mail.png) |

---

## 🏗️ 项目架构

```
chatgpt-register/
├── main.go                  # 入口：Gin 路由注册 + 静态文件嵌入
├── internal/
│   ├── auth/                # JWT 鉴权服务（单 token、自动续期、落库）
│   ├── codexreg/            # Grok 注册入口（subprocess 调 python）
│   │   ├── python.go        # 调 pyreg/grok_register.py + 验证码回填协议
│   │   ├── codex.go         # 用拿到的 sso 组装 auth 数据
│   │   └── codexreg.go      # 注册任务入口
│   ├── db/                  # SQLite 数据库初始化（纯 Go 驱动，无需 CGO）
│   ├── emailalias/          # 邮箱地址规整
│   ├── handlers/            # HTTP 接口层（Gin Handler）
│   │   ├── auth.go          # 登录 / 改密接口
│   │   ├── registration.go  # 账户 CRUD + 日志 + 截图接口
│   │   ├── produce.go       # 批量生产控制（启动 / 状态 / 停止）
│   │   ├── mailbox.go       # 邮箱 CRUD + 取件接口
│   │   ├── proxy.go         # 代理测试接口
│   │   └── settings.go      # 系统设置接口
│   ├── mailfetch/           # 邮件取件（自动读取验证码）
│   ├── models/              # GORM 数据模型（Admin / Registration / Mailbox / Setting）
│   └── producer/            # 批量注册调度器（并发控制）
├── pyreg/                   # python 注册脚本（nodriver）
│   └── grok_register.py     # 无头跑完 accounts.x.ai 注册，提取 sso
└── static/                  # 前端静态页面（嵌入二进制，无需 Web 服务器）
    ├── dashboard.html        # 仪表盘
    ├── accounts.html/js      # 账户管理
    ├── mailboxes.html/js     # 邮箱管理
    ├── settings.html         # 系统设置
    ├── login.html            # 登录页
    ├── layout.js             # 公共布局 / 侧边栏
    └── style.css             # 毛玻璃主题 CSS（35KB 精心打磨）
```

**技术栈：** Go · Gin · GORM · SQLite（纯 Go 驱动）· python + nodriver · curl_cffi · JWT · 原生 H5

---

## 🚀 快速开始

### 方式一：直接运行（推荐）

下载 Release 中对应系统的可执行文件，双击运行或：

```bash
# Windows
./chatgpt-register.exe

# Linux
./chatgpt-register-linux
```

浏览器打开 [http://localhost:9000](http://localhost:9000)

### 方式二：源码运行

```bash
git clone https://github.com/yourname/chatgpt-register
cd chatgpt-register
go run .
```

### 方式三：自行编译

```bash
# Windows
go build -o chatgpt-register.exe .

# Linux
GOOS=linux go build -o chatgpt-register-linux .
```

### 自定义端口

```bash
ADDR=8080 ./chatgpt-register.exe
```

> 数据保存在同目录 `adskull.db`，已加入 `.gitignore`，请勿提交。

---

## 🔐 登录

- 默认账号：`admin` / `admin123`
- 首次登录后请立即在「系统设置」修改密码（密码长度 > 6 位）

**JWT 安全机制：**
- Token 有效期 **24 小时**，签发超过 2 小时自动续期（响应头 `X-New-Token` 下发）
- Token 全局唯一：重新登录 / 改密 / 续期均会使旧 Token 立即失效
- Token 落库持久化，进程重启后无需重新登录

---

## 📋 功能说明

### 批量生产（核心功能）

1. 在「邮箱管理」导入邮箱（支持批量 CSV 导入）
2. 在「系统设置」配置并发数、代理池
3. 在「仪表盘」点击「生产」，设置目标数量，一键启动
4. 实时查看进度、成功率、执行日志和注册截图

**注册策略：** 每个已验证邮箱注册一个账号（用邮箱本身地址）。注册失败自动补单直到达标。

### 邮箱管理

- 状态四态：`待验证 / 验证中 / 验证失败 / 已验证`
- 「取件」弹窗：3 秒轮询实时收件，sandbox iframe 隔离展示邮件内容
- 支持 Outlook（需填 `client_id` + `refresh_token`，Microsoft Graph API）

---

## ⚙️ 使用指南

### 第一步：导入邮箱

进入「邮箱管理」，支持两种方式导入：

- **手动添加**：填写邮箱地址、密码、服务商
- **批量导入**：点击「批量导入邮箱」，每行一条，格式：
  ```
  email----password----provider
  ```
  `provider` 支持 `outlook` / `hotmail` / `gmail` 等

> Outlook 邮箱需额外填写 `client_id` 和 `refresh_token`（用于 Microsoft Graph API 自动收件）

---

### 第二步：配置系统设置

进入「系统设置」，配置以下参数后保存：

| 参数 | 说明 | 建议值 |
|------|------|--------|
| 并发数 | 同时注册的账号数量 | 3 ~ 5 |
| 无头模式 | 是否隐藏浏览器窗口 | 生产环境建议开启 |
| 代理池 | 每行一个代理，格式见下方 | 按需配置 |

**代理格式：**
```
http://user:pass@ip:port
socks5://user:pass@ip:port
http://ip:port
```

---

### 第三步：启动批量生产

1. 进入「仪表盘」，点击右上角「**空跑**」按钮先测试环境
2. 点击「**生产**」，输入目标账号数量
3. 系统自动调度：给每个已验证邮箱注册一个账号 → 失败自动补单直到达标
4. 实时查看成功数 / 失败数 / 进度条

---

### 查看注册详情

- 进入「账户管理」点击任意账号可查看**实时执行日志**（步骤级别，精确到秒）
- 点击「截图」可查看注册过程中的**浏览器截图**，方便排查失败原因
- 支持按状态筛选：待注册 / 注册中 / 已注册 / 注册失败

---

## ❓ 常见问题

**Q：浏览器第一次启动很慢？**
> A：首次运行会自动下载 Chromium（约 150MB），下载完成后后续启动秒开。

**Q：注册失败怎么办？**
> A：系统会自动重试补单，无需手动干预。查看执行日志可定位具体失败原因（如验证码超时、IP 被封等）。

**Q：不配置代理可以用吗？**
> A：可以，留空即直连。但大量并发注册建议配置代理池，避免 IP 被限流。

**Q：账号导出格式是什么？**
> A：在「账户管理」勾选账号后点击「导出」，导出为 CSV 格式，字段包含邮箱 / 密码 / 用户名 / 状态 / 备注。

---

## ⭐ 如果觉得好用，欢迎 Star！

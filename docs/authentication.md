# 用户鉴权与登录会话

面板鉴权采用短期 Access Token、由前端保存并每次续期都轮换的 Refresh Token，以及服务端登录会话控制面的组合。面板不使用任何鉴权 Cookie，前端和后端可以部署在不同的域名上（见下文“前后端分开部署”）。面板请求不再依赖 Gin session，也不再要求 `New-Api-User` 请求头。

## 鉴权模型

- Access Token 是有效期 15 分钟的 JWT，只保存在浏览器内存中，通过 `Authorization: Bearer <token>` 发送。
- Refresh Token 是随机不透明值（`<sid>.<secret>`），所属会话最长有效 30 天。登录和续期接口在响应体的 `refresh_token` 里返回它，前端保存在 localStorage 的 `new_api_refresh_token` 中，续期和登出时放在请求体里发回。服务端仅保存 HMAC 摘要，每次续期都换发新的 Refresh Token。
- 前端以“是否保存着 Refresh Token”判断冷启动时是否值得续期，不再需要会话提示 Cookie。
- `user_sessions` 是登录会话控制面，记录设备、IP、登录方式、最后活跃时间、到期时间和撤销状态。数据库中的 Session 状态是最终权威；撤销传播速度取决于下文所述的 Redis 拓扑。
- 用户的密码、状态、角色或安全因子发生安全相关变化时，`auth_version` 会递增并使旧登录会话失效。订阅带来的分组升降级只刷新授权缓存，不会退出任何登录设备。
- Redis 缓存保存用户鉴权快照和登录会话快照。版本栅栏和撤销 tombstone 防止旧缓存重新授权；Session 快照使用跟随 `SYNC_FREQUENCY` 的短 TTL，缓存未命中或未启用 Redis 时回退到数据库校验。

`SESSION_SECRET` 用于派生 Access Token、Security Proof、Refresh Token 摘要和 AuthFlow 摘要的不同用途密钥。生产环境及多节点部署必须在所有节点配置相同的高强度随机值；更换该值会使现有登录、临时鉴权流程和 Security Proof 全部失效。

## 多节点 Redis 拓扑

多节点部署必须共用同一主数据库。登录 Session、账户级活跃 Session 上限和签发窗口计数都以数据库为权威，因此这些限制在应用节点间全局生效。Redis 中的 Session Hash（包含 `revoking`/`revoked` tombstone）只是缓存，其 TTL 为 Session 剩余寿命与有效 `SYNC_FREQUENCY` 中的较小值；`SYNC_FREQUENCY` 默认及非法值回退均为 `60` 秒。读取缓存不会续期，过期后会按 SID 回源数据库。延迟完成的 active 缓存回写只能使用其数据库观察窗口尚未消耗的 TTL，不能在撤销 tombstone 到期后重新启动一个完整缓存周期。

| Redis 部署方式 | Session 状态传播 | 限流语义 |
| --- | --- | --- |
| 所有节点共享 Redis | 正常撤销和版本发布通过同一缓存即时传播 | Redis 限流额度在所有节点间共享 |
| 每个节点使用独立 Redis | 最迟在该节点 Session 缓存 TTL 到期后回源收敛，即不超过有效 `SYNC_FREQUENCY`；版本轮换期间，新 Token 在持有旧缓存的节点上可能短暂返回 401 | 每个节点独立计数，集群总额度最坏约为单节点阈值乘以节点数 |
| 不使用 Redis | 每次 Session 校验直接读取数据库 | 使用各节点的内存限流器，额度同样按节点独立 |

`SYNC_FREQUENCY` 越大，独立 Redis 部署的陈旧窗口越长；值越小，每个活跃 SID 在每个节点上回源数据库的频率越高。默认配置下，持续活跃的 Session 每个节点最多约每 60 秒增加一次数据库主键点查。共享 Redis 时，撤销 tombstone 和版本发布仍保持即时传播。

所有节点必须使用相同的 `SESSION_SECRET`。当多个节点连接同一个 Redis 时，还必须使用相同的 `CRYPTO_SECRET`，否则节点生成的缓存键摘要不一致，无法正确共享缓存。上述保证只覆盖登录 Session 鉴权的有界陈旧语义；限流额度及其他 Redis 缓存仍会受到 Redis 拓扑影响，不能据此认为整个控制面与拓扑无关。

## 浏览器接口

登录成功后，密码登录、2FA、Passkey、OAuth、WeChat 和 Telegram 登录均返回统一数据：

```json
{
  "success": true,
  "data": {
    "access_token": "...",
    "token_type": "Bearer",
    "access_expires_at": 1730000000,
    "refresh_token": "<sid>.<secret>",
    "user": {},
    "session": {
      "sid": "...",
      "current": true,
      "login_method": "password",
      "ip": "...",
      "user_agent": "...",
      "created_at": 1730000000,
      "last_active_at": 1730000000,
      "expires_at": 1732592000
    }
  }
}
```

会话相关接口：

| 接口 | 鉴权 | 用途 |
| --- | --- | --- |
| `POST /api/user/auth/refresh` | 请求体 `{"refresh_token": "..."}` | 轮换 Refresh Token 并签发新的 Access Token |
| `POST /api/user/auth/logout` | Bearer，或请求体里的 Refresh Token | 撤销当前登录会话；前端随后删除自己保存的 Refresh Token |
| `GET /api/user/sessions` | Bearer | 查看当前鉴权版本的有效登录会话，当前会话优先，最多 100 条 |
| `DELETE /api/user/sessions/:sid` | Bearer | 撤销指定登录会话，包括当前会话 |
| `POST /api/user/sessions/revoke-others` | Bearer | 保留当前会话并撤销其他会话 |

客户端内存中已有会话时，应在 refresh/logout 请求中发送 `X-Auth-Session: <sid>`。请求里的 Refresh Token 与该 SID 不一致时（例如另一个标签页登录了别的账户，localStorage 里的令牌已被替换），两个端点都返回 `409 AUTH_SESSION_MISMATCH`，且不会轮换或撤销任何会话；客户端先通过 refresh 清除本标签页的旧 SID、恢复当前保存的令牌对应的会话，再重试 logout。冷启动尚无内存会话时可以省略该请求头。

并发使用同一个 Refresh Token 时，服务端通过确定性轮换恢复同一个后继 Token，多个浏览器标签页不会因丢失“胜者”响应而被迫退出。最近一代 Refresh Token 在短暂容错窗口结束后再次出现会撤销对应会话；无法识别的更早代或随机 Token 只会被拒绝，不会允许攻击者凭猜测踢掉会话。

前端使用 Web Locks 串行化同一浏览器配置文件中的刷新，每次续期都在锁内重新读取 localStorage 里的 Refresh Token，因为别的标签页可能刚刚换发过；并通过 BroadcastChannel（不支持时回退到 `storage` 事件）仅同步会话标识和登录/退出事件。Access Token 只保存在各标签页的内存中，不会持久化，也不会通过跨标签页消息传递。

前端将冷启动状态与登录状态分开管理。网络或服务端临时故障允许后续导航重试 refresh；服务端拒绝 Refresh Token（401）时，前端删除发出的那个令牌并进入已完成的匿名状态。会话结束（登出、撤销当前会话、注销账户、续期被拒）时才删除保存的令牌，并且只删除属于该会话的令牌，另一个标签页新登录保存的令牌不受影响；仅本标签页的状态重置（如 SID 不一致、续期竞争）不删除令牌。内存 SID 与令牌所属 SID 不一致时，客户端清除旧内存身份并在不携带旧 SID 的情况下重试一次。

公开页面的冷启动在内存和 localStorage 里都没有身份时跳过 refresh，直接按匿名渲染，且**不**把这次跳过记为已完成的匿名判定。会依据鉴权结果做跳转的位置（受保护路由与登录页）内存为空时一律回源。

### 安全取舍：Refresh Token 由前端保存

这是为支持前后端分开部署（不同站点之间的 Cookie 会被浏览器当作第三方 Cookie 拦截）所做的有意取舍，相比此前的 HttpOnly Cookie 有所削弱：

- 同源脚本可以读取 localStorage。页面一旦出现 XSS，攻击者可以拿走 Refresh Token 并在别处续期，最长到会话 30 天的绝对期限；HttpOnly Cookie 只允许攻击者在页面打开期间借用会话。OWASP Session Management Cheat Sheet 建议不要把 Refresh Token 放进 Web Storage，ASVS 5.0 的 14.3.3 允许浏览器存储里保存会话令牌。
- 缓解措施：每次续期都轮换 Refresh Token；已被替换的令牌在 30 秒宽限期后再次出现会撤销整个会话（ASVS 10.4.5），因此被盗令牌和合法客户端谁先续期，另一方再续期时就会让会话失效；会话最长 30 天（ASVS 10.4.8）；用户可在“登录会话”里查看并撤销任何会话（ASVS 10.4.9），修改密码等安全变更会撤销其余会话；Access Token 只在内存里、15 分钟过期；登出时服务端撤销会话、前端删除令牌（ASVS 7.4.1、14.3.1）。
- 不再有凭据随浏览器自动发送，续期、登出和登录接口不再需要 CSRF 防护和 Origin 校验。
- 控制台页面带内容安全策略（见下文），注入的内联脚本和外站脚本无法执行，降低出现 XSS 的可能。
- 未实现的加固：把 Refresh Token 绑定到不可导出的 WebCrypto 密钥（DPoP，RFC 9449），或改用后端代为保存令牌的 BFF 模式（RFC 10017）。

### 内容安全策略（CSP）

控制台页面（`index.html` 以及回落到它的前端路由）带 `Content-Security-Policy`（ASVS 5.0 3.4.3、3.4.6）。策略由控制台仓库 new-api-frontend 的构建生成（`src/lib/static-hosting.ts`），写进构建产物的 `_headers`，由 Cloudflare Pages 等静态主机发送；后端不提供控制台，也不发送这个头：

```text
default-src 'self'; script-src 'self' https://telegram.org https://challenges.cloudflare.com https://js.stripe.com https://*.js.stripe.com; style-src 'self' 'unsafe-inline'; img-src * data: blob:; media-src * data: blob:; font-src 'self' data:; connect-src * data: blob:; frame-src *; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'
```

- 脚本只能来自控制台自己的 Origin 和用到的第三方：Telegram 登录组件、Cloudflare Turnstile、Stripe.js。不允许 `'unsafe-inline'` 和 `'unsafe-eval'`。构建产物里没有内联脚本：主题初始化和语言包预加载脚本由构建输出为带哈希的文件（`static/js/theme-bootstrap.*.js`、`static/js/locale-preload.*.js`），zod 关闭了 JIT（`src/lib/zod-config.ts`），因此策略是固定的，不随构建变化。
- 统计脚本由控制台构建时的环境变量 `UMAMI_WEBSITE_ID`、`UMAMI_SCRIPT_URL`、`GOOGLE_ANALYTICS_ID` 注入，它们的 Origin 自动加入 `script-src`；Google Analytics 的配置也输出为文件，不是内联脚本。后端同名的环境变量不再生效。
- 样式允许内联（组件会注入 `<style>`）。图片、媒体、iframe 和网络请求允许任意来源，因为管理员可以配置任意 Logo、首页和关于页 iframe、聊天链接、OIDC 发现地址，接口也在另一个 Origin 上。
- 页面只允许同源嵌入（`frame-ancestors 'self'`），不能加载插件，不能修改 base URL，表单只能提交到本站。
- 未配置违规上报（ASVS 3.4.7，L3）。

## Session 签发限额与保留策略

服务端在所有登录方式的统一 Session 签发出口执行两级账户限制：

- `USER_SESSION_ACTIVE_LIMIT`（默认 `50`）：单用户未过期且状态为 active 的 Session 上限。达到上限时新登录返回 `409 AUTH_SESSION_LIMIT`。
- `USER_SESSION_ISSUANCE_LIMIT`（默认 `100`）和 `USER_SESSION_ISSUANCE_WINDOW_SECONDS`（默认 `86400`）：统计窗口内该用户创建的所有 Session，包含已撤销和旧鉴权版本的记录。达到上限时返回 `429 AUTH_SESSION_ISSUANCE_LIMIT`。
- 这两次计数与插入不加跨数据库锁；极端并发登录可能出现少量超额，但计数失败会拒绝签发，不会降级放行。

升级时已经超过活跃上限的账户不会被自动下线或挤掉旧会话；限制只作用于后续的新 Session 签发。

`USER_SESSION_REVOKED_RETENTION_DAYS`（默认 `7`）控制 revoked 行的审计保留期。签发计数依赖窗口内的行仍存在，因此签发窗口不得超过 revoked 保留期。如果配置超出，启动时会记录告警并将实际窗口钳制到保留期，避免提前删除 revoked 行导致限流计数被低估。

定时清理即使发现 `expires_at` 已过期，也不会删除 `created_at` 仍落在实际签发窗口内的行；尚未达到 revoked 保留期的撤销记录同样会继续保留。这样在扩大配置窗口时，过期清理不会静默削弱签发计数或审计保留。

活跃数量会计入状态仍为 active 但 `user_auth_version` 已过期的异常残留行，而设备列表只展示当前鉴权版本。因此遇到 `AUTH_SESSION_LIMIT` 时，应优先在仍已登录的设备上执行“撤销其他会话”，该操作会同时清理不可见的旧版本 active 行；没有可用设备时可使用密码重置撤销所有会话。密码重置不会清空签发窗口计数。

仅 master 节点每小时分批删除过期 Session 和超过保留期的 revoked Session。`USER_SESSION_HOURLY_ALERT_THRESHOLD`（默认 `5000`）只在最近一小时全局签发量异常时记录告警，不会形成可被滥用的全站登录拒绝开关。

## 前后端分开部署

后端不包含也不提供控制台。控制台是独立仓库 [new-api-frontend](https://github.com/Herta-Network/new-api-frontend)，构建产物部署在 Cloudflare Pages 等静态主机上，部署步骤见它的 README。能让前端主机把 `/api`、`/pg` 反向代理到后端时（例如 Nginx 同时托管构建产物和代理接口，或 Cloudflare Pages 上控制台自带的 Pages Function），页面和接口同源，不涉及 CORS。不转发时，由浏览器直接访问另一个域名的后端。后端只需要知道一个地址，即系统设置「前端地址」，初始化时会自动填好：

1. **前端的后端地址**：`index.html` 旁边的 `runtime-config.json`，例如 `{"apiBaseUrl": "https://api.example.com"}`。前端启动前读取它，所有接口请求、日志导出、Ollama 拉取流和 playground 的 SSE 都发往这个地址；文件缺失、无效或为空时请求保持同源。构建时设置环境变量 `API_BASE_URL` 会写入这个文件（Cloudflare Pages 在项目的构建变量里设置）；能直接修改托管文件的主机也可以改这个文件，不用重新构建。
2. **系统设置「前端地址」**：控制台的公开地址，只填 Origin。浏览器里完成初始化向导时，后端把发起请求的控制台 Origin 记为「前端地址」（此前未设置时）；用 curl 等不带 Origin 的方式初始化，或控制台换了域名，就在系统设置里手动填写。它的 Origin 自动进入 CORS 白名单；重置密码链接、邮件里的链接、支付完成返回页、OAuth 回调地址（`<前端地址>/oauth/<provider>`）和默认的 Passkey 域名都使用它；不填时回落到「服务器地址」。「服务器地址」只表示后端：AI 接口地址、支付回调、Midjourney 图片和视频等后端提供的文件都用它。在后端域名上打开的非接口路径（例如旧邮件链接、登记在后端地址上的 OAuth 回调）会带着路径和查询参数 302 重定向到「前端地址」，未设置时返回 404。环境变量 `FRONTEND_BASE_URL` 不再使用，设置了它时启动日志会提示。
3. **可选的额外来源**：环境变量 `FRONTEND_ORIGINS`，逗号分隔的精确 Origin（协议、主机和端口，不带路径、通配符或域名后缀匹配），除「前端地址」外还要放行的控制台来源，例如 Cloudflare Pages 的预览地址。一般不需要设置。

CORS 规则：

- `/api` 和 `/pg`（控制台接口）只对「前端地址」和 `FRONTEND_ORIGINS` 的 Origin 返回 `Access-Control-Allow-Origin`（回显该 Origin，附 `Vary: Origin`），不返回 `Access-Control-Allow-Credentials`，预检只允许控制台用到的请求头（`Authorization`、`Content-Type`、`Cache-Control`、`X-Security-Proof`、`X-Auth-Session`、`X-OAuth-Nonce`）。不受信任的 Origin 不会得到任何 CORS 头，其预检返回 403。
- 系统初始化完成前还不知道控制台的地址：此时 `GET /api/setup`、`POST /api/setup` 和 `GET /api/status` 对任意 Origin 返回 `Access-Control-Allow-Origin: *`，其它控制台接口照旧拒绝。这三个接口的响应不含敏感信息（ASVS 5.0 3.4.2）。初始化完成后这个例外关闭；多节点部署中，在初始化之前启动的其它节点要重启后才会关闭。已知的取舍：初始化前，网络上能访问到这个实例的人本来就能用 curl 完成初始化；放开 CORS 后，员工浏览器里打开的任意网页也能对内网中尚未初始化的实例发起初始化。部署后请立即完成初始化，或初始化之后再开放网络访问。
- AI 接口（`/v1`、`/v1beta`、`/mj`、任务插件路由、`/dashboard/billing`）以及 `/api/usage/token`、`/api/log/token` 用 API Key 鉴权，继续允许任意 Origin，浏览器里直接用 `sk-` 密钥调用的场景不受影响。

域名相关的其它设置：

- OAuth：state 存在 `auth_flows` 表里，不依赖 Cookie。各提供方后台登记的回调地址要改为 `<前端地址>/oauth/<provider>`。
- Passkey：WebAuthn 在前端页面上执行，RP ID 必须是前端域名本身或其上级域名。修改「前端地址」时，原来的隐式域名会保留为旧域名，已有 Passkey 继续可用（初始化时自动记录的除外，那时还没有 Passkey）；前端换到另一个主域名时，在旧域名上注册的 Passkey 无法在新域名使用，这是 WebAuthn 的限制。
- Telegram：在 BotFather 里用 `/setdomain` 设置前端域名。
- Turnstile：在 Cloudflare 后台把前端主机名加入组件允许的主机名列表。

同域部署（前端主机同时反向代理 `/api`、`/pg` 到后端）：

```env
SESSION_SECRET=<high-entropy-random-value>
# FRONTEND_ORIGINS 不需要设置；runtime-config.json 的 apiBaseUrl 留空；「前端地址」由初始化记为这个域名
```

不同域名部署，例如 Cloudflare Pages（控制台 `https://app.example.com`，后端 `https://api.example.com`）：

```env
SESSION_SECRET=<high-entropy-random-value>
# FRONTEND_ORIGINS 不需要设置：在 https://app.example.com 上完成初始化时，它被记为「前端地址」
```

```json
{ "apiBaseUrl": "https://api.example.com" }
```

（Cloudflare Pages 上即构建变量 `API_BASE_URL=https://api.example.com`。）

并在系统设置里把「服务器地址」设为 `https://api.example.com`，确认「前端地址」是 `https://app.example.com`。

Cloudflare Pages 同源转发：控制台的 Pages Function（new-api-frontend 的 `functions/`）把 `/api`、`/pg` 转发到后端。Pages 变量设 `API_PROXY_URL=https://api.example.com`，不设 `API_BASE_URL`，`runtime-config.json` 保持空值；后端的环境变量和系统设置与上面相同。函数把访客地址写进 `X-Real-IP`，控制台和后端的域名在同一个 Cloudflare zone 时，Cloudflare 把它作为发往后端的 `CF-Connecting-IP`，后端服务器按下文「经过 Cloudflare」还原访客地址。通过 `*.pages.dev` 访问时子请求跨 zone，Cloudflare 把 `CF-Connecting-IP` 固定为 `2a06:98c0:3600::103`，所有这类访客共用一个地址，正式使用自定义域名。

Cloudflare Pages 和 Netlify 读取构建产物里的 `_headers`，自动发送上文的内容安全策略。其它静态服务器要自己给 `index.html`（以及回落到它的所有前端路由）加上 `_headers` 里的 `Content-Security-Policy`，例如 Nginx：

```nginx
location / {
    try_files $uri /index.html;
    add_header Content-Security-Policy "<dist/_headers 里的策略>" always;
}
```

`SESSION_COOKIE_SECURE` 和 `SESSION_COOKIE_TRUSTED_URL` 已废弃：前者不再生效；后者的值会并入 `FRONTEND_ORIGINS`。设置了它们时启动日志会提示。

## 可信代理与 IP 限流

Gin 默认会信任所有代理提供的客户端 IP 请求头。本项目改为兼顾常见反代拓扑和公网直连安全的三态配置：

- 未配置、空字符串或纯空白的 `TRUSTED_PROXIES` 默认信任 `127.0.0.0/8`、`::1`、`10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16` 和 `fc00::/7`，并输出启动告警。该默认值覆盖同机 Nginx、Docker Compose 和常见内网反代；公网直连地址不在列表中，其伪造的 `X-Forwarded-For` 会被忽略。
- `TRUSTED_PROXIES=none`（大小写不敏感且必须单独使用）启用严格直连模式，不信任任何代理，`ClientIP()` 只使用 TCP 直连地址。
- 其他非空值按英文逗号解析为代理 IP/CIDR，并完全替代默认列表。应填写反向代理自身的地址而不是客户端网段；非法 CIDR、空列表或将 `none` 与其他值混用都会阻止服务启动。

Gin 只在请求的直连来源属于可信代理时解析客户端 IP 请求头，并从转发链右侧向左寻找首个非可信地址。因此常见 Nginx `$proxy_add_x_forwarded_for` 链中的公网客户端地址会阻止更左侧的伪造前缀生效。默认信任私网的残余风险是：能够从同一私网直接访问应用的其他机器或容器仍可伪造这些请求头；需要消除此风险时应使用 `none` 或配置精确代理地址。

### 经过 Cloudflare

后端经过 Cloudflare 代理时（包括控制台的 Pages Function 转发），Nginx 看到的直连地址是 Cloudflare 的节点，访客地址在 `CF-Connecting-IP` 头里。让 Nginx 从这个头还原访客地址，它传给后端的 `X-Real-IP`、`X-Forwarded-For` 才是访客本人。这需要 `ngx_http_realip_module`（`nginx -V` 的输出含 `--with-http_realip_module`，发行版自带的 Nginx 都包含）。在 `http` 段加入：

```nginx
# Cloudflare Start：只信任 Cloudflare 的地址段，https://www.cloudflare.com/ips/
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
set_real_ip_from 103.22.200.0/22;
set_real_ip_from 103.31.4.0/22;
set_real_ip_from 141.101.64.0/18;
set_real_ip_from 108.162.192.0/18;
set_real_ip_from 190.93.240.0/20;
set_real_ip_from 188.114.96.0/20;
set_real_ip_from 197.234.240.0/22;
set_real_ip_from 198.41.128.0/17;
set_real_ip_from 162.158.0.0/15;
set_real_ip_from 104.16.0.0/13;
set_real_ip_from 104.24.0.0/14;
set_real_ip_from 172.64.0.0/13;
set_real_ip_from 131.0.72.0/22;
set_real_ip_from 2400:cb00::/32;
set_real_ip_from 2606:4700::/32;
set_real_ip_from 2803:f800::/32;
set_real_ip_from 2405:b500::/32;
set_real_ip_from 2405:8100::/32;
set_real_ip_from 2a06:98c0::/29;
set_real_ip_from 2c0f:f248::/32;
real_ip_header CF-Connecting-IP;
# Cloudflare End
```

不要使用 `set_real_ip_from 0.0.0.0/0`：那会让任何客户端伪造自己的地址。只列 Cloudflare 的地址段时，绕过 Cloudflare 直连源站的请求带来的 `CF-Connecting-IP` 会被忽略。Cloudflare 偶尔调整地址段，可以把上面的内容放进单独的文件（Debian 和 Ubuntu 的 `nginx.conf` 在 `http` 段包含 `/etc/nginx/conf.d/*.conf`），用 cron 定期重新生成：

```sh
#!/bin/sh
set -eu
v4=$(curl -fsS https://www.cloudflare.com/ips-v4)
v6=$(curl -fsS https://www.cloudflare.com/ips-v6)
conf=/etc/nginx/conf.d/cloudflare-realip.conf
{
  echo '# 由脚本生成，来自 https://www.cloudflare.com/ips/'
  for ip in $v4 $v6; do echo "set_real_ip_from $ip;"; done
  echo 'real_ip_header CF-Connecting-IP;'
} > "$conf.new"
mv "$conf.new" "$conf"
nginx -t && nginx -s reload
```

后端的 `server` 段把还原后的地址传给后端，并为 SSE、WebSocket 和较大的请求体留出余量：

```nginx
# 放在 http 段：WebSocket（/v1/realtime、/v1/responses）需要。
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl;
    server_name api.example.com;
    # ssl_certificate、ssl_certificate_key：例如 Cloudflare 源站证书

    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        # $remote_addr 已是访客地址；不转发客户端自带的 X-Forwarded-For。
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        # 流式响应逐块转发，长时间生成不被切断。
        proxy_buffering off;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

`TRUSTED_PROXIES` 只填 Nginx 到后端这一跳的地址，保持默认即可：Nginx 与后端同机、Docker 只把端口发布在 `127.0.0.1` 上时，后端看到的直连地址是回环或 Docker 网桥网关，都在默认列表里。不要把 Cloudflare 的地址段填进 `TRUSTED_PROXIES`，后端并不直接接收 Cloudflare 的连接。还可以只让 Cloudflare 回源：防火墙只对上面的地址段开放 443，或启用 Cloudflare 的 Authenticated Origin Pulls。

配置后，Nginx 访问日志的第一列和控制台「登录会话」里的 IP 应是访客的公网地址。看到 Cloudflare 节点的地址，说明 `set_real_ip_from` 没有生效；看到 `2a06:98c0:3600::103`，说明请求来自跨 zone 的 Worker，例如通过 `*.pages.dev` 访问的控制台。

Redis 限流使用原子 Lua 固定窗口，替代旧的近似滑动窗口 List 实现。这是有意的语义变化：窗口边界两侧可分别打满一次，极短时间内通过量最高约为配置值的两倍。例如 `20 次/20 分钟` 在边界可通过约 40 次。帐户级 Session 上限和签发窗口继续控制数据库增长；如未来需要严格抑制边界突发，需单独迁移为 ZSET 滑动窗口。

用户级模型成功请求限流仍使用原有 Redis List 近似滑动窗口，但列表时间戳统一写为 UTC。滚动升级期间，旧节点写入的本地时间字符串和新节点写入的 UTC 字符串无法从格式上区分，可能在一个模型限流窗口内临时误放行或误拒绝。所有节点升级完成并经过一个完整窗口后会自然收敛；本次升级不会切换 Key 或主动删除现有列表。

开放注册仍会受 Critical IP 限流保护，但分布式 IP 多账号攻击不能仅靠 IP 限流阻止。公网开放注册的部署应同时启用 Turnstile 和邮箱验证；更强的设备或多维风控需作为独立安全项目设计。

## PAT 调用契约

面板 PAT（个人访问令牌）通过 `Authorization: Bearer <pat>` 发送，也兼容单值 `Authorization: <pat>`；不接受 URL 参数。`New-Api-User` 不参与鉴权。

- 令牌格式为 `nap_` 加 43 位 base62 随机串（约 256 bit）。明文只在创建时返回一次，数据库只保存 SHA-256 摘要，该摘要同时作为审计记录中的 `token_ref`。
- 每个用户最多 20 个令牌，存放在 `user_access_tokens` 表。创建时必须选择 scope 和过期时间：默认 30 天，最短 1 小时，也允许永不过期（界面会提示风险）。过期令牌立即失效，超过保留期后由 master 节点清理。
- scope 形如 `resource:action`。个人权限（资料、API 令牌、用量、钱包、账户安全）和部分后台权限由令牌目录定义；受 Casbin 保护的后台权限直接复用管理员权限矩阵的 resource 与 action。只能授予自己当前拥有的权限，并且每个请求都会重新读取角色和权限：撤回某项管理员权限后，携带该 scope 的令牌立即失去对应能力。
- 每条面板路由都必须声明所需 scope。未声明的路由一律拒绝（403 `ACCESS_TOKEN_ROUTE_UNDECLARED`），scope 不足返回 403 `ACCESS_TOKEN_SCOPE_DENIED`，令牌过期返回 401 `ACCESS_TOKEN_EXPIRED`。
- PAT 不能创建、改名或撤销 PAT，也不能管理登录会话；这些接口只接受浏览器登录会话。
- PAT 可以完成二次验证，Proof 绑定到该令牌，并且令牌必须持有该操作对应的 scope：`channel.key.read` 需要 `channel:secret_view`，`admin.user.*` 需要 `user:write`，本人的密码、2FA、Passkey、账号绑定和注销需要 `account_security:write`。PAT 不能使用 OAuth 验证方式。
- 通过 PAT 完成的安全变更（如修改密码、2FA、Passkey）会让该用户的所有浏览器登录会话下线，令牌本身保持可用。
- 用户被禁用期间，其令牌全部不可用，重新启用后恢复；注销或删除用户时，其令牌被删除。
- 最后使用时间和 IP 每个令牌每分钟最多更新一次；更新失败不影响请求。

## 临时鉴权流程与二次验证

OAuth state、2FA pending、Passkey ceremony、Telegram bind 等临时状态存放在 `auth_flows`。客户端只持有随机 `flow_token`，数据库仅保存 HMAC 摘要；流程具有用途、provider、intent、用户和登录会话绑定，并且只能原子消费一次。OAuth 注册的 affiliate code 也随登录 AuthFlow 保存。

OAuth 登录的 state 绑定发起登录的浏览器标签页（ASVS 10.1.2）：前端生成 32 字节随机 nonce，随 `POST /api/oauth/state` 提交，服务端只在 AuthFlow 里保存其 SHA-256；前端把 nonce 存在本标签页的 sessionStorage 里，回调时放在 `X-OAuth-Nonce` 请求头交回。nonce 不一致或缺失时回调返回 403 且不消费 state，因此攻击者把自己发起的回调链接发给别人，无法让对方登录攻击者的账户。绑定和验证流程本就要求发起时的登录会话，不需要 nonce。

标准 OAuth 绑定回调由 popup 通过同源 `postMessage` 交给 opener；只有 opener 使用自身内存中的 Bearer 调用后端绑定接口。Telegram 绑定先由已登录前端创建绑定 AuthFlow，再让 widget 回调携带路径中的 `flow_token`，回调时会重新确认原登录会话仍有效。Telegram 的已签名 widget assertion 也会登记为一次性凭据，重复回放会被拒绝。

敏感操作使用有效期 1 分钟的 `X-Security-Proof`：

- `channel.key.read`：查看渠道密钥；
- `passkey.register`：注册 Passkey；
- `passkey.delete`：删除 Passkey。

管理员对其他用户执行的高风险操作同样要求 Proof，且 scope 的 context 绑定被操作用户，签发给某个用户的 Proof 不能改用于另一个用户或另一种动作：

- `admin.user.delete`（`{"user_id"}`）：`DELETE /api/user/:id` 与 `POST /api/user/manage` 的 `delete`；
- `admin.user.manage`（`{"user_id","action"}`，action 为 `disable` / `enable` / `promote` / `demote`）：`POST /api/user/manage`；额度调整 `add_quota` 不要求 Proof；
- `admin.user.update`（`{"user_id"}`）：`PUT /api/user/` 在请求包含新密码或 `admin_permissions` 时要求；仅修改显示名、分组、备注不要求；
- `admin.user.create`（`{"role"}`）：`POST /api/user/` 创建管理员角色时要求；创建普通用户不要求；
- `admin.user.passkey.reset`（`{"user_id"}`）：`DELETE /api/user/:id/reset_passkey`；
- `admin.user.2fa.disable`（`{"user_id"}`）：`DELETE /api/user/:id/2fa`；
- `admin.user.binding.clear`（`{"user_id","binding_type"}` 或 `{"user_id","provider_id"}`）：`DELETE /api/user/:id/bindings/:binding_type` 与 `DELETE /api/user/:id/oauth/bindings/:provider_id`。

这些 `admin.user.*` scope 只对管理员及以上角色签发；已启用 2FA 或 Passkey 的管理员必须使用其中之一，未启用时回退到密码（或已绑定的 OAuth）重新认证；密码登录被关闭时不接受密码验证。通过 PAT 调用上述接口时，令牌需持有 `user:write` 并完成二次验证；旧版令牌不能调用。

Proof 同时绑定用户、登录会话、用户鉴权版本、会话版本和 scope，不能跨用户、跨会话或跨用途复用。对 PAT 而言，Proof 绑定的“登录会话”就是令牌本身。

注册 Passkey 时，register begin 与 finish 都必须携带有效的 `passkey.register` Proof；finish 会在消费一次性 AuthFlow 之前重新验证 Proof。已启用 2FA 或 Passkey 的用户用其中之一取得 Proof；两者都未启用的首次注册同样要求 Proof，有密码的用户用密码重新认证，没有密码的用户用已绑定的 OAuth 账号重新认证。

## 升级注意事项

- 旧 `session` Cookie 不再使用；升级后现有面板登录会失效，用户需要重新登录。
- Refresh Token 改由前端保存。升级前登录的会话仍持有 HttpOnly 的 `new_api_refresh` Cookie：前端没有保存令牌时，续期请求不带请求体，后端在请求来自控制台的 Origin（后端自己的主机或前端来源）时接受该 Cookie 续期一次，返回新的 Refresh Token，并删除 `new_api_refresh` 和 `new_api_has_session` 两个 Cookie。其它带有这两个 Cookie 的续期、登出和登录响应也会删除它们。
- 升级时正在进行中的 OAuth 登录（state 创建于升级前，没有 nonce）会失败一次，重新点击登录即可。
- 前后端分开部署时，OAuth 提供方登记的回调地址要改为前端地址，见“前后端分开部署”。
- 数据库迁移会新增 `user_sessions`、`auth_flows`、`external_identity_claims` 和 `users.auth_version`，并为已有用户初始化鉴权版本、回填 Telegram 账号唯一归属；若历史数据中同一 Telegram ID 已绑定多个用户，迁移会拒绝继续启动，需先消除歧义。
- 数据库迁移会为 Session 签发计数和分批清理新增索引；已有 `user_sessions` 很大时应为首次启动预留维护窗口。
- `user_sessions.previous_refresh_hash` 会从定长 `char(64)` 迁移为 `varchar(64)`。应用会兼容读取历史定长字段留下的空格填充；迁移后的目标结构必须保持幂等，连续启动不应反复执行列类型变更。
- 仅 master 节点定时清理过期登录会话、超过配置保留期的 revoked 会话和已过保留期的 AuthFlow。
- 未配置 `TRUSTED_PROXIES` 时会兼容信任回环和常见私网代理；使用公网负载均衡器、`100.64.0.0/10`、链路本地地址或自定义 CNI 网段的部署仍需显式配置。需要严格忽略所有转发头时设置为 `none`。
- Redis 限流从近似滑动窗口改为原子固定窗口，存在明确的边界双倍突发语义。
- 用户级模型成功请求限流的 UTC 时间戳在滚动升级期间存在一个窗口的混合格式过渡，期间可能临时误放行或误拒绝。
- 自建客户端应按新的 AuthBundle、`flow_token` 和 Security Proof 契约升级；PAT 客户端可直接移除 `New-Api-User`。
- 旧版 PAT 保留在 `users.access_token` 中，自新版首次启动起 30 天内照常可用，之后自动停用并返回 401 `ACCESS_TOKEN_LEGACY_RETIRED`。停用日期记录在 `options` 表的 `LegacyAccessTokenRetireAt`，由服务端写入，不能通过设置接口修改。过渡期内不能再生成旧版令牌，但可以撤销。
- 删除了 `/api/user/token*` 接口，改为 `/api/user/access_tokens`。
- 降级到旧版本后，新令牌不可用；未撤销的旧版令牌会重新可用，因为旧版本不认停用日期。

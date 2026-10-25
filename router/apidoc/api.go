// Package apidoc generates the Swagger documents in docs/openapi from the
// swag annotations on the handlers: api.json for the web console API under
// /api and relay.json for the AI API. api.go and relay.go hold each
// document's general information; `make openapi` regenerates both, and the
// package test fails when they are out of date or miss a registered route.
package apidoc

// swag finds the types the annotations name among the dependencies of the
// package holding the general information, so this package depends on
// everything the router does.
import _ "github.com/QuantumNous/new-api/router"

//	@title			new-api 管理接口
//	@version		1.0
//	@description	new-api 控制台和管理后台使用的接口，路径都在 /api 下。
//	@description
//	@description	响应：除另有说明外，响应体是 common.ApiResponse：success 表示是否成功，message 是提示文本，data 是数据。业务失败通常仍返回 HTTP 200，success 为 false；鉴权失败返回 401 或 403，并带 code（如 AUTH_UNAUTHORIZED）。
//	@description
//	@description	鉴权：Authorization: Bearer <访问令牌>。访问令牌是登录得到的 15 分钟 JWT（用 POST /api/user/auth/refresh 凭 new_api_refresh Cookie 续期），或在安全设置里创建的个人访问令牌（PAT）。UserAuth、AdminAuth、RootAuth 是同一个请求头，区别在要求的角色。删除账户、绑定账号等敏感操作还要带 X-Security-Proof，先用 POST /api/verify 完成二次验证取得。
//	@BasePath		/
//	@accept			json
//	@produce		json

//	@securityDefinitions.apikey	UserAuth
//	@in							header
//	@name						Authorization
//	@description				已登录用户：Bearer <访问令牌>，访问令牌是登录得到的 JWT 或个人访问令牌（PAT）。

//	@securityDefinitions.apikey	AdminAuth
//	@in							header
//	@name						Authorization
//	@description				管理员或超级管理员：Bearer <访问令牌>。

//	@securityDefinitions.apikey	RootAuth
//	@in							header
//	@name						Authorization
//	@description				超级管理员：Bearer <访问令牌>。

//	@securityDefinitions.apikey	SecurityProof
//	@in							header
//	@name						X-Security-Proof
//	@description				二次验证凭证，由 POST /api/verify 签发，绑定用户、登录会话和操作范围，短时有效。

//	@securityDefinitions.apikey	TokenAuth
//	@in							header
//	@name						Authorization
//	@description				API 密钥：Bearer sk-<密钥>。

//	@tag.name			系统
//	@tag.description	初始化、站点状态、公告与协议、公开的定价和排行
//	@tag.name			认证
//	@tag.description	注册、登录、二次登录验证、续期与登出、找回密码
//	@tag.name			个人
//	@tag.description	当前用户的资料、设置、分组、模型、邀请和充值
//	@tag.name			登录会话
//	@tag.name			个人访问令牌
//	@tag.name			两步验证
//	@tag.name			Passkey
//	@tag.name			安全验证
//	@tag.description	敏感操作前的二次验证，签发 X-Security-Proof
//	@tag.name			第三方登录
//	@tag.description	OAuth、微信、Telegram 登录与账号绑定
//	@tag.name			签到
//	@tag.name			令牌
//	@tag.description	API 密钥的管理和用量查询
//	@tag.name			订阅
//	@tag.name			支付
//	@tag.name			用户管理
//	@tag.name			订阅管理
//	@tag.name			支付管理
//	@tag.description	支付订单和无法匹配的回调
//	@tag.name			支付设置
//	@tag.description	支付方式、充值选项和汇率
//	@tag.name			渠道
//	@tag.name			模型
//	@tag.name			供应商
//	@tag.name			分组
//	@tag.name			兑换码
//	@tag.name			日志
//	@tag.description	使用日志和审计日志
//	@tag.name			数据统计
//	@tag.name			任务
//	@tag.description	异步任务和 Midjourney 任务记录
//	@tag.name			运营概览
//	@tag.name			权限
//	@tag.name			系统设置
//	@tag.name			邮件
//	@tag.name			自定义 OAuth
//	@tag.name			任务插件
//	@tag.name			倍率同步
//	@tag.name			系统任务
//	@tag.name			系统信息
//	@tag.description	节点实例和运行性能
//	@tag.name			部署
//	@tag.description	io.net 模型部署

package handler

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/internal/module/payment"

	"github.com/gin-gonic/gin"
)

// paymentNotifyBodyLimit caps a gateway callback; every gateway sends far
// less.
const paymentNotifyBodyLimit = 64 << 10

// PaymentNotify receives the callbacks of every gateway at
// /api/payment/notify/{platform}/{token}. It reads the payload the way the
// platform delivers it and answers the way the platform expects.
//
//	@Summary		接收支付网关回调
//	@Description	由支付网关调用，客户端不调用。EPay 以 GET 查询参数或 POST 表单回调，Stripe（签名在 Stripe-Signature 请求头）和 Cryptomus 以 POST JSON 回调。EPay 和 Cryptomus 收到纯文本 success 或 fail，Stripe 收到 JSON，其 success 为 true 或 false；验签通过但无法结算订单的支付记为无法匹配的支付，仍回 200。
//	@Tags			支付
//	@Accept			json,x-www-form-urlencoded
//	@Produce		plain,json
//	@Param			platform			path		string	true	"网关平台：epay、stripe 或 cryptomus"
//	@Param			token				path		string	true	"支付渠道回调地址中的密钥段"
//	@Param			Stripe-Signature	header		string	false	"Stripe 回调的签名"
//	@Success		200					{string}	string	"已处理"
//	@Failure		400					{string}	string	"回调无效，重发也不会成功"
//	@Failure		404					{string}	string	"平台或回调地址不存在"
//	@Failure		500					{string}	string	"暂时失败，网关应重试"
//	@Router			/api/payment/notify/{platform}/{token} [get]
//	@Router			/api/payment/notify/{platform}/{token} [post]
func PaymentNotify(c *gin.Context) {
	platform := c.Param("platform")
	style, ok := payment.NotifyStyle(platform)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	n := payment.Notification{HTTPMethod: c.Request.Method}
	invalid := payment.NotifyReply(style, payment.ErrInvalidCallback)
	if style.Body {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, paymentNotifyBodyLimit+1))
		if err != nil || len(body) > paymentNotifyBodyLimit {
			c.Data(invalid.Status, invalid.ContentType, []byte(invalid.Body))
			return
		}
		n.Body = body
		if style.SignatureHeader != "" {
			n.Signature = c.GetHeader(style.SignatureHeader)
		}
	} else {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, paymentNotifyBodyLimit)
		if err := c.Request.ParseForm(); err != nil {
			c.Data(invalid.Status, invalid.ContentType, []byte(invalid.Body))
			return
		}
		n.Form = c.Request.Form
		n.Params = make(map[string]string, len(n.Form))
		for key, values := range n.Form {
			if style.UniqueParams && len(values) != 1 {
				c.Data(invalid.Status, invalid.ContentType, []byte(invalid.Body))
				return
			}
			n.Params[key] = values[0]
		}
	}
	err := payment.HandleNotify(c.Request.Context(), platform, c.Param("token"), n, c.ClientIP())
	reply := payment.NotifyReply(style, err)
	c.Data(reply.Status, reply.ContentType, []byte(reply.Body))
}

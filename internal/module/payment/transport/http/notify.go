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

package payment

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/module/payment/entity"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/gateway"
	"github.com/QuantumNous/new-api/internal/module/payment/internal/store"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// sendTopupReceipt emails the buyer the receipt of a top-up order this
// process just credited. Each order is credited once, under its lock, so
// the receipt goes out once; callers call it only after the crediting
// transaction committed. It runs off the request and only reads: a buyer
// without an email address gets none, and a failure is logged and leaves
// the order as it is. Receipts are off while the email setting
// topup_receipt_enabled is.
func sendTopupReceipt(order *entity.PaymentOrder) {
	if order == nil || order.Purpose != entity.PaymentPurposeTopup || !system_setting.GetEmailSetting().TopupReceiptEnabled {
		return
	}
	credited := *order
	runInBackground(func() {
		user, err := model.GetUserById(credited.UserId, false)
		if err != nil {
			common.SysError(common.LogText("failed to send the receipt of payment order %s: %v", credited.OrderNo, err))
			return
		}
		if user.Email == "" {
			return
		}
		names, err := store.OptionNames([]int{credited.PaymentOptionId})
		if err != nil {
			common.SysError(common.LogText("failed to send the receipt of payment order %s: %v", credited.OrderNo, err))
			return
		}
		paid, paidCurrency := credited.PaymentAmount, credited.PaymentCurrency
		if paidCurrency == "" {
			paid, paidCurrency = credited.Amount, credited.Currency
		}
		lang := user.GetSetting().Language
		err = common.SendTemplateEmail(lang, user.Email, common.TemplateEmail{
			Kind: common.EmailKindTopupReceipt,
			Vars: map[string]string{
				"OrderNo": credited.OrderNo,
				"Time":    i18n.FormatTime(lang, time.Unix(credited.FinishedAt, 0)),
				"Method":  names[credited.PaymentOptionId],
				"Amount":  operation_setting.CurrencySymbol(paidCurrency) + gateway.FormatAmount(paid),
				"Credit":  logger.FormatQuotaToCents(credited.QuotaAmount),
				"Balance": logger.FormatQuotaToCents(user.Quota),
				"Link":    service.ConsoleURL(service.WalletOrdersPath),
			},
		})
		if err != nil {
			common.SysError(common.LogText("failed to send the receipt of payment order %s: %v", credited.OrderNo, err))
		}
	})
}

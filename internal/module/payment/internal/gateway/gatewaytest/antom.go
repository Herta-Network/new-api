package gatewaytest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// antomKeys are the merchant's key, Antom's key and an impostor's key of
// every fake Antom, generated once per test binary.
var antomKeys = sync.OnceValue(func() [3]*rsa.PrivateKey {
	var keys [3]*rsa.PrivateKey
	for i := range keys {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		keys[i] = key
	}
	return keys
})

// FakeAntom is an in-process Antom API answering the pay, inquiryPayment
// and cancel calls of the payment gateway under the production and the
// sandbox paths. It verifies each request's signature with the merchant's
// public key, failing the test when one does not verify, and signs its
// answers and notifications with Antom's key. Payments are kept by their
// request id, so asking to pay one again returns it as Antom does.
type FakeAntom struct {
	t   testing.TB
	URL string
	// ClientID is the client the fake serves; a SANDBOX_ one is served
	// under the sandbox paths.
	ClientID string
	// MerchantKey signs the gateway's requests, AntomKey the fake's answers
	// and notifications; ImpostorKey is neither.
	MerchantKey, AntomKey, ImpostorKey *rsa.PrivateKey

	mu       sync.Mutex
	payments map[string]*antomPayment
	requests []AntomRequest
	answers  AntomAnswers
	// payHook changes each answer to pay before it is sent.
	payHook func(answer map[string]any)
	// refuse answers each call to the API with this failure code.
	refuse map[string]string
}

// AntomRequest is a request the fake received.
type AntomRequest struct {
	API  string
	Body map[string]any
}

// AntomAnswers is how the fake signs its answers: with Key instead of
// Antom's, a Response-Time Skew away from now, or a body Tampered with after
// signing.
type AntomAnswers struct {
	Key      *rsa.PrivateKey
	Skew     time.Duration
	Tampered bool
}

type antomPayment struct {
	id, status string
	refunded   bool
	amount     map[string]any
	answer     map[string]any
}

// NewFakeAntom starts a fake Antom API of a sandbox client for the test.
func NewFakeAntom(t testing.TB) *FakeAntom {
	t.Helper()
	keys := antomKeys()
	fake := &FakeAntom{
		t: t, ClientID: "SANDBOX_5X00000000000000", MerchantKey: keys[0], AntomKey: keys[1], ImpostorKey: keys[2],
		payments: map[string]*antomPayment{}, refuse: map[string]string{},
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.URL = server.URL
	return fake
}

// PrivateKey is key as Antom Dashboard shows a merchant's: Base64 PKCS #8.
func PrivateKey(key *rsa.PrivateKey) string {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// PublicKey is key's public half as Antom Dashboard shows Antom's: Base64
// PKIX.
func PublicKey(key *rsa.PrivateKey) string {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// Config is the configuration of an Antom payment method of the fake's
// client collecting wallet in currency and settling in USD.
func (f *FakeAntom) Config(wallet, currency string) string {
	config, err := common.Marshal(map[string]string{
		"client_id": f.ClientID, "private_key": PrivateKey(f.MerchantKey), "platform_public_key": PublicKey(f.AntomKey),
		"region": "asia", "payment_method_type": wallet, "currency": currency, "settlement_currency": "USD",
	})
	if err != nil {
		panic(err)
	}
	return string(config)
}

// SetAnswers changes how the fake signs its answers.
func (f *FakeAntom) SetAnswers(answers AntomAnswers) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = answers
}

// SetPayHook changes each answer to pay with hook before it is sent.
func (f *FakeAntom) SetPayHook(hook func(answer map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payHook = hook
}

// Refuse answers every call to api with a failure of code; an empty code
// stops refusing.
func (f *FakeAntom) Refuse(api, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse[api] = code
}

// SetStatus sets the status of the payment of requestID, as the buyer or
// the wallet would.
func (f *FakeAntom) SetStatus(requestID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payment(requestID).status = status
}

// Refund records a successful refund of the payment of requestID.
func (f *FakeAntom) Refund(requestID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payment(requestID).refunded = true
}

// Payment returns the id and status of the payment of requestID.
func (f *FakeAntom) Payment(requestID string) (id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payment := f.payment(requestID)
	return payment.id, payment.status
}

// Requests returns the bodies of the requests to api.
func (f *FakeAntom) Requests(api string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var bodies []map[string]any
	for _, request := range f.requests {
		if request.API == api {
			bodies = append(bodies, request.Body)
		}
	}
	return bodies
}

func (f *FakeAntom) payment(requestID string) *antomPayment {
	payment, ok := f.payments[requestID]
	if !ok {
		f.t.Fatalf("the fake Antom has no payment %s", requestID)
	}
	return payment
}

// Notification is the payment result notification of requestID's payment:
// resultStatus S for a paid one, F for a failed one.
func (f *FakeAntom) Notification(requestID, resultStatus string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	payment := f.payment(requestID)
	result := map[string]any{"resultCode": "SUCCESS", "resultStatus": "S", "resultMessage": "success"}
	if resultStatus == "F" {
		result = map[string]any{"resultCode": "ORDER_IS_CLOSED", "resultStatus": "F", "resultMessage": "The order is closed."}
	}
	return map[string]any{
		"notifyType": "PAYMENT_RESULT", "result": result, "paymentRequestId": requestID, "paymentId": payment.id,
		"paymentAmount": maps.Clone(payment.amount), "paymentCreateTime": "2026-11-17T12:01:00+08:00",
	}
}

// Notify signs a notification body with Antom's key as delivered now to
// path; it returns the request header and body.
func (f *FakeAntom) Notify(path string, notification map[string]any) (http.Header, []byte) {
	body, err := common.Marshal(notification)
	if err != nil {
		f.t.Fatalf("encode notification: %v", err)
	}
	return f.Sign(f.AntomKey, time.Now().Format(time.RFC3339), path, body), body
}

// Sign returns the header of a notification of body to path signed with
// key at stamp.
func (f *FakeAntom) Sign(key *rsa.PrivateKey, stamp, path string, body []byte) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Client-Id", f.ClientID)
	header.Set("Request-Time", stamp)
	header.Set("Signature", "algorithm=RSA256,keyVersion=1,signature="+signAntom(key, path, f.ClientID, stamp, body))
	return header
}

func signAntom(key *rsa.PrivateKey, path, clientID, stamp string, body []byte) string {
	digest := sha256.Sum256(append([]byte("POST "+path+"\n"+clientID+"."+stamp+"."), body...))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return url.QueryEscape(base64.StdEncoding.EncodeToString(signature))
}

func (f *FakeAntom) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read Antom request: %v", err)
	}
	clientID := r.Header.Get("Client-Id")
	prefix := "/ams/api/v1/payments/"
	if strings.HasPrefix(clientID, "SANDBOX_") {
		prefix = "/ams/sandbox/api/v1/payments/"
	}
	api, ok := strings.CutPrefix(r.URL.Path, prefix)
	if r.Method != http.MethodPost || !ok || clientID != f.ClientID {
		f.t.Errorf("unexpected Antom request %s %s of client %q", r.Method, r.URL.Path, clientID)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := f.verifyRequest(r, body); err != nil {
		f.t.Errorf("Antom %s request: %v", api, err)
		// Antom does not sign the refusal of a request it cannot verify.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"resultCode":"INVALID_SIGNATURE","resultStatus":"F","resultMessage":"The signature is invalid."}}`))
		return
	}
	var request map[string]any
	if err := common.Unmarshal(body, &request); err != nil {
		f.t.Errorf("decode Antom %s request: %v", api, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, AntomRequest{API: api, Body: request})
	answer := f.answer(api, request)
	content, err := common.Marshal(answer)
	if err != nil {
		f.t.Errorf("encode Antom answer: %v", err)
	}
	key := f.answers.Key
	if key == nil {
		key = f.AntomKey
	}
	responseTime := time.Now().Add(f.answers.Skew).Format(time.RFC3339)
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Client-Id", clientID)
	w.Header().Set("Response-Time", responseTime)
	w.Header().Set("Signature", "algorithm=RSA256,keyVersion=1,signature="+signAntom(key, r.URL.Path, clientID, responseTime, content))
	if f.answers.Tampered {
		// Still JSON, no longer what was signed.
		content = append(content, ' ')
	}
	_, _ = w.Write(content)
}

// verifyRequest checks a request's signature with the merchant's public key
// and its millisecond Request-Time.
func (f *FakeAntom) verifyRequest(r *http.Request, body []byte) error {
	stamp := r.Header.Get("Request-Time")
	milliseconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || time.Since(time.UnixMilli(milliseconds)).Abs() > time.Minute {
		return fmt.Errorf("Request-Time %q is not the current Unix time in milliseconds", stamp)
	}
	signatureHeader := r.Header.Get("Signature")
	_, encoded, ok := strings.Cut(signatureHeader, "signature=")
	if !ok || !strings.HasPrefix(signatureHeader, "algorithm=RSA256,") {
		return fmt.Errorf("malformed Signature header %q", signatureHeader)
	}
	unescaped, err := url.QueryUnescape(encoded)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(unescaped)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(append([]byte("POST "+r.URL.Path+"\n"+r.Header.Get("Client-Id")+"."+stamp+"."), body...))
	return rsa.VerifyPKCS1v15(&f.MerchantKey.PublicKey, crypto.SHA256, digest[:], signature)
}

func antomResult(status, code, message string) map[string]any {
	return map[string]any{"result": map[string]any{"resultStatus": status, "resultCode": code, "resultMessage": message}}
}

// answer handles a verified request under the lock.
func (f *FakeAntom) answer(api string, request map[string]any) map[string]any {
	if code := f.refuse[api]; code != "" {
		return antomResult("F", code, "refused by the test")
	}
	requestID, _ := request["paymentRequestId"].(string)
	payment, exists := f.payments[requestID]
	switch api {
	case "pay":
		amount, _ := request["paymentAmount"].(map[string]any)
		if exists {
			// Like Antom, the same request id returns the same payment while
			// it can be paid.
			switch {
			case !reflect.DeepEqual(payment.amount, amount):
				return antomResult("F", "REPEAT_REQ_INCONSISTENT", "The amount or currency is different from the previous request.")
			case payment.status == "FAIL" || payment.status == "CANCELLED":
				return antomResult("F", "ORDER_IS_CLOSED", "The order is closed.")
			}
			return payment.answer
		}
		id := fmt.Sprintf("20261117194010800100188%012d", len(f.payments)+1)
		code := "https://global.alipay.com/2810020400" + id[len(id)-12:]
		answer := antomResult("U", "PAYMENT_IN_PROCESS", "payment in process")
		answer["paymentRequestId"], answer["paymentId"], answer["paymentAmount"] = requestID, id, maps.Clone(amount)
		answer["paymentCreateTime"] = time.Now().Format(time.RFC3339)
		answer["normalUrl"] = "https://render.alipay.com/p/s/i/?code=" + url.QueryEscape(code)
		answer["schemeUrl"] = "alipays://platformapi/startApp?qrcode=" + url.QueryEscape(code)
		answer["orderCodeForm"] = map[string]any{
			"expireTime": time.Now().Add(14 * time.Minute).Format(time.RFC3339),
			"codeDetails": []map[string]any{
				{"codeValue": "https://global.alipay.com/merchant/order/showQrImage.htm?code=" + url.QueryEscape(code), "displayType": "BIGIMAGE"},
				{"codeValue": code, "displayType": "TEXT"},
			},
		}
		if f.payHook != nil {
			f.payHook(answer)
		}
		f.payments[requestID] = &antomPayment{id: id, status: "PROCESSING", amount: maps.Clone(amount), answer: answer}
		return answer
	case "inquiryPayment":
		if !exists {
			return antomResult("F", "ORDER_NOT_EXIST", "The order does not exist.")
		}
		answer := antomResult("S", "SUCCESS", "Success")
		answer["paymentStatus"], answer["paymentRequestId"], answer["paymentId"] = payment.status, requestID, payment.id
		answer["paymentAmount"] = maps.Clone(payment.amount)
		if payment.refunded {
			answer["transactions"] = []map[string]any{{"transactionType": "REFUND", "transactionStatus": "SUCCESS", "transactionAmount": maps.Clone(payment.amount)}}
		}
		return answer
	case "cancel":
		if !exists {
			return antomResult("F", "ORDER_NOT_EXIST", "The order does not exist.")
		}
		// Like Antom, a failed payment stays failed and a paid one is
		// refunded.
		if payment.status != "FAIL" {
			payment.status = "CANCELLED"
		}
		answer := antomResult("S", "SUCCESS", "Success")
		answer["paymentRequestId"], answer["paymentId"] = requestID, payment.id
		return answer
	}
	f.t.Errorf("unexpected Antom API %s", api)
	return antomResult("F", "NO_INTERFACE_DEF", "API is not defined.")
}

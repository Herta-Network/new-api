package gateway

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// EPay V2 signs every request with the merchant's RSA private key, and the
// gateway signs its answers and callbacks with its own: SHA-256 with PKCS #1
// v1.5, in Base64, over the scalar parameters except sign, sign_type and
// blank values, sorted by name and joined as k=v&k=v. Signed messages carry
// a Unix timestamp in seconds, which must be within epayV2ClockSkew.
const epayV2ClockSkew = 5 * time.Minute

var (
	errEPayV2PrivateKey error = common.NewMessage("The EPay V2 merchant private key must be an unencrypted RSA private key of at least 2048 bits")
	errEPayV2PublicKey  error = common.NewMessage("The EPay V2 platform public key must be an RSA public key of at least 2048 bits")
)

var (
	epayParamName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	epayInteger   = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// epayV2Keys are the keys of an EPay V2 merchant, with the DER they were
// read from.
type epayV2Keys struct {
	private    *rsa.PrivateKey
	public     *rsa.PublicKey
	privateDER []byte
	publicDER  []byte
}

// parseEPayV2Keys reads the merchant's private key (PKCS #8 or PKCS #1) and
// the gateway's public key (PKIX or PKCS #1), each as PEM or as the bare
// Base64 DER merchant consoles show.
func parseEPayV2Keys(privateKey, publicKey string) (*epayV2Keys, error) {
	keys := &epayV2Keys{}
	var err error
	if keys.privateDER, err = epayKeyDER(privateKey); err != nil {
		return nil, errEPayV2PrivateKey
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(keys.privateDER); err == nil {
		keys.private, _ = parsed.(*rsa.PrivateKey)
	} else {
		keys.private, _ = x509.ParsePKCS1PrivateKey(keys.privateDER)
	}
	if keys.private == nil || keys.private.N.BitLen() < 2048 {
		return nil, errEPayV2PrivateKey
	}
	if keys.publicDER, err = epayKeyDER(publicKey); err != nil {
		return nil, errEPayV2PublicKey
	}
	if parsed, err := x509.ParsePKIXPublicKey(keys.publicDER); err == nil {
		keys.public, _ = parsed.(*rsa.PublicKey)
	} else {
		keys.public, _ = x509.ParsePKCS1PublicKey(keys.publicDER)
	}
	if keys.public == nil || keys.public.N.BitLen() < 2048 {
		return nil, errEPayV2PublicKey
	}
	return keys, nil
}

// epayKeyDER decodes a key. A PEM key pasted into a one-line field loses its
// line breaks, so the armor is cut off instead of parsed; an encrypted key
// keeps headers in its body and does not decode.
func epayKeyDER(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "-----BEGIN "); ok {
		_, body, found := strings.Cut(rest, "-----")
		if !found {
			return nil, errors.New("malformed PEM key")
		}
		if body, _, found = strings.Cut(body, "-----END "); !found {
			return nil, errors.New("malformed PEM key")
		}
		text = body
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	if err != nil || len(der) == 0 {
		return nil, errors.New("the key is not Base64 DER")
	}
	return der, nil
}

// epayV2Canonical is the text an EPay V2 signature covers.
func epayV2Canonical(params map[string]string) (string, error) {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if !epayParamName.MatchString(key) {
			return "", fmt.Errorf("invalid EPay parameter name %q", key)
		}
		if key == "sign" || key == "sign_type" || strings.Trim(value, " \t\n\r\x00\x0b") == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var text strings.Builder
	for i, key := range keys {
		if i > 0 {
			text.WriteByte('&')
		}
		text.WriteString(key + "=" + params[key])
	}
	return text.String(), nil
}

// signV2 returns params with the merchant id, the timestamp and the
// signature, as the form to send.
func (g *epayGateway) signV2(params map[string]string) (url.Values, error) {
	signed := make(map[string]string, len(params)+3)
	for key, value := range params {
		if value != "" {
			signed[key] = value
		}
	}
	signed["pid"] = g.config.PID
	signed["timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)
	signed["sign_type"] = "RSA"
	canonical, err := epayV2Canonical(signed)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(canonical))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.v2.private, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sign the EPay request: %w", err)
	}
	form := url.Values{}
	for key, value := range signed {
		form.Set(key, value)
	}
	form.Set("sign", base64.StdEncoding.EncodeToString(signature))
	return form, nil
}

// verifyV2 checks a message the gateway signed: its signature, a timestamp
// within epayV2ClockSkew, and the merchant id when it names one. The
// sign_type defaults to RSA and may be left out; the signature does not
// cover it.
func (g *epayGateway) verifyV2(params map[string]string) error {
	if signType := params["sign_type"]; signType != "" && !strings.EqualFold(signType, "RSA") {
		return fmt.Errorf("unsupported signature type %q", signType)
	}
	if params["sign"] == "" {
		return errors.New("the message is not signed")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(params["sign"])
	if err != nil || len(signature) == 0 {
		return errors.New("verify sign failed")
	}
	canonical, err := epayV2Canonical(params)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(canonical))
	if rsa.VerifyPKCS1v15(g.v2.public, crypto.SHA256, digest[:], signature) != nil {
		return errors.New("verify sign failed")
	}
	stamp := params["timestamp"]
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if len(stamp) != 10 || strings.Trim(stamp, "0123456789") != "" || err != nil {
		return errors.New("the signed timestamp is not in Unix seconds")
	}
	if age := time.Since(time.Unix(seconds, 0)); age > epayV2ClockSkew || age < -epayV2ClockSkew {
		return errors.New("the signed timestamp is outside the permitted clock skew")
	}
	if pid, ok := params["pid"]; ok && pid != g.config.PID {
		return errors.New("merchant id mismatch")
	}
	return nil
}

// callV2 posts a signed request to the API path and returns the scalar
// fields of the gateway's successful answer once its signature verifies.
// Nested values are not signed and are left out. A failure the gateway
// signed must verify as well; an unsigned failure is only reported.
//
// NiuNiu Pay signs every answer but that of a payment creation, despite its
// documentation and SDK, so an unsigned successful answer of api/pay/create
// is taken over HTTPS, as far as TLS can be trusted. It only leads the buyer
// to the payment: a payment settles on a signed callback or query alone.
func (g *epayGateway) callV2(ctx context.Context, path string, params map[string]string) (map[string]string, error) {
	endpoint, err := g.endpoint(path)
	if err != nil {
		return nil, err
	}
	form, err := g.signV2(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create EPay %s request failed", path)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	body, err := g.send(req, "EPay "+path)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("decode EPay %s answer: %w", path, err)
	}
	answer := make(map[string]string, len(fields))
	for key, raw := range fields {
		raw = bytes.TrimSpace(raw)
		switch {
		case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
		case len(raw) > 0 && raw[0] == '"':
			var text string
			if err := common.Unmarshal(raw, &text); err != nil {
				return nil, fmt.Errorf("decode EPay %s answer: %w", path, err)
			}
			answer[key] = text
		case string(raw) == "null" || string(raw) == "false":
			// PHP reads these as empty strings, which the signature leaves out.
			answer[key] = ""
		case string(raw) == "true":
			answer[key] = "1"
		case epayInteger.Match(raw):
			answer[key] = string(raw)
		default:
			return nil, fmt.Errorf("EPay %s answer has a value in %s that cannot be signed", path, key)
		}
	}
	code := answer["code"]
	if !epayInteger.MatchString(code) {
		return nil, fmt.Errorf("EPay %s answer has no code", path)
	}
	_, signed := fields["sign"]
	switch {
	case signed:
		if err := g.verifyV2(answer); err != nil {
			return nil, fmt.Errorf("EPay %s answer: %w", path, err)
		}
	case code != "0":
		// An unsigned failure is only reported.
	case path != "api/pay/create":
		return nil, fmt.Errorf("EPay %s answer is not signed", path)
	case endpoint.Scheme != "https":
		return nil, fmt.Errorf("EPay %s answer is not signed, which is accepted only over HTTPS", path)
	}
	if code != "0" {
		return nil, Refused(fmt.Errorf("EPay %s failed: code=%s msg=%q", path, code, answer["msg"]))
	}
	return answer, nil
}

// v2PageAction builds the signed redirect to the gateway's payment page.
// The signature is timestamped, so the redirect is built anew each time.
func (g *epayGateway) v2PageAction(params map[string]string) (*Action, error) {
	endpoint, err := g.endpoint("api/pay/submit")
	if err != nil {
		return nil, err
	}
	form, err := g.signV2(params)
	if err != nil {
		return nil, err
	}
	endpoint.RawQuery = form.Encode()
	return &Action{Type: ActionURL, URL: endpoint.String()}, nil
}

// createV2Payment creates the payment at /api/pay/create, asking for a
// payment page (method=jump) or, when qrCode is set, for what a computer
// pays with (method=web, device=pc); the gateway may answer with a QR code,
// a page or an app link.
func (g *epayGateway) createV2Payment(ctx context.Context, params map[string]string, qrCode bool) (*Action, error) {
	params["method"] = "jump"
	if qrCode {
		params["method"], params["device"] = "web", "pc"
	}
	answer, err := g.callV2(ctx, "api/pay/create", params)
	if err != nil {
		return nil, err
	}
	if answer["trade_no"] == "" {
		return nil, fmt.Errorf("EPay payment answer of order %s has no trade number", params["out_trade_no"])
	}
	return epayPayAction(answer["pay_type"], answer["pay_info"], qrCode)
}

func (g *epayGateway) queryV2Order(ctx context.Context, orderNo string) (*epayQueryResult, error) {
	answer, err := g.callV2(ctx, "api/pay/query", map[string]string{"out_trade_no": orderNo})
	if err != nil {
		return nil, err
	}
	if answer["out_trade_no"] != orderNo {
		return nil, fmt.Errorf("EPay query answer names order %q, not %s", answer["out_trade_no"], orderNo)
	}
	// Refunded (2), frozen (3) and pre-authorized (4) orders are neither
	// paid nor awaiting payment.
	return &epayQueryResult{
		merchantID:  answer["pid"],
		tradeNo:     answer["trade_no"],
		orderNo:     answer["out_trade_no"],
		paymentType: answer["type"],
		money:       answer["money"],
		paid:        answer["status"] == "1",
		unpaid:      answer["status"] == "0",
	}, nil
}

// closeV2Order closes an unpaid order at the gateway, which then takes no
// payment for it. Some payment channels cannot close orders.
func (g *epayGateway) closeV2Order(ctx context.Context, orderNo string) error {
	_, err := g.callV2(ctx, "api/pay/close", map[string]string{"out_trade_no": orderNo})
	return err
}

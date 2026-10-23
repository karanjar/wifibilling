package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"wifibilling/config"
)

type Mpesa struct {
	cfg    *config.Config
	client *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func NewMpesa(cfg *config.Config) *Mpesa {
	return &Mpesa{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

func (m *Mpesa) baseURL() string {
	if m.cfg.MpesaEnv == "production" {
		return "https://api.safaricom.co.ke"
	}
	return "https://sandbox.safaricom.co.ke"
}

func (m *Mpesa) accessToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Now().Before(m.expiry) {
		return m.token, nil
	}
	req, _ := http.NewRequest("GET", m.baseURL()+"/oauth/v1/generate?grant_type=client_credentials", nil)
	req.SetBasicAuth(m.cfg.MpesaConsumerKey, m.cfg.MpesaConsumerSecret)
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("mpesa auth failed: %s", string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("mpesa auth: bad response")
	}
	m.token = out.AccessToken
	m.expiry = time.Now().Add(50 * time.Minute)
	return m.token, nil
}

type STKResponse struct {
	MerchantRequestID   string `json:"MerchantRequestID"`
	CheckoutRequestID   string `json:"CheckoutRequestID"`
	ResponseCode        string `json:"ResponseCode"`
	ResponseDescription string `json:"ResponseDescription"`
	CustomerMessage     string `json:"CustomerMessage"`
}

// STKPush prompts the customer's phone to enter their M-Pesa PIN.
func (m *Mpesa) STKPush(phone string, amount int, accountRef, desc string) (*STKResponse, error) {
	tok, err := m.accessToken()
	if err != nil {
		return nil, err
	}
	ts := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString([]byte(m.cfg.MpesaShortcode + m.cfg.MpesaPasskey + ts))

	payload := map[string]any{
		"BusinessShortCode": m.cfg.MpesaShortcode,
		"Password":          password,
		"Timestamp":         ts,
		"TransactionType":   "CustomerPayBillOnline", // use CustomerBuyGoodsOnline for a Till
		"Amount":            amount,
		"PartyA":            phone,
		"PartyB":            m.cfg.MpesaShortcode,
		"PhoneNumber":       phone,
		"CallBackURL":       fmt.Sprintf("%s/api/v1/mpesa/callback/%s", m.cfg.MpesaCallbackBase, m.cfg.MpesaCallbackSecret),
		"AccountReference":  accountRef,
		"TransactionDesc":   desc,
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", m.baseURL()+"/mpesa/stkpush/v1/processrequest", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out STKResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("stk push: bad response: %s", string(body))
	}
	if resp.StatusCode != 200 || out.ResponseCode != "0" {
		return nil, fmt.Errorf("stk push rejected: %s", string(body))
	}
	return &out, nil
}

// ---- Callback payload ----

type CallbackItem struct {
	Name  string `json:"Name"`
	Value any    `json:"Value"`
}

type STKCallback struct {
	Body struct {
		StkCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  struct {
				Item []CallbackItem `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// Meta returns the receipt number and amount paid from a successful callback.
func (c *STKCallback) Meta() (receipt string, amount float64) {
	for _, it := range c.Body.StkCallback.CallbackMetadata.Item {
		switch it.Name {
		case "MpesaReceiptNumber":
			receipt, _ = it.Value.(string)
		case "Amount":
			amount, _ = it.Value.(float64)
		}
	}
	return
}

var phoneRe = regexp.MustCompile(`^254[17]\d{8}$`)

// NormalizePhone converts 07xx / 01xx / +254... into 254XXXXXXXXX.
func NormalizePhone(p string) (string, bool) {
	p = strings.NewReplacer(" ", "", "-", "", "+", "").Replace(p)
	switch {
	case len(p) == 10 && p[0] == '0':
		p = "254" + p[1:]
	case len(p) == 9 && (p[0] == '7' || p[0] == '1'):
		p = "254" + p
	}
	if !phoneRe.MatchString(p) {
		return "", false
	}
	return p, true
}

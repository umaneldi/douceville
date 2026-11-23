package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const mollieBaseURL = "https://api.mollie.com/v2"

// MollieClient interroge l'API Payments de Mollie. Le mode (test ou live)
// dépend uniquement de la clé : test_… ou live_….
type MollieClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewMollieClient crée un client Mollie ; baseURL vide : API publique.
func NewMollieClient(apiKey, baseURL string, httpClient *http.Client) *MollieClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = mollieBaseURL
	}
	return &MollieClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// Mode implémente PaymentProvider.Mode.
func (c *MollieClient) Mode() string {
	if strings.HasPrefix(c.apiKey, "live_") {
		return "live"
	}
	return "test"
}

type mollieAmount struct {
	Currency string `json:"currency"`
	Value    string `json:"value"`
}

type molliePayment struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Amount   mollieAmount      `json:"amount"`
	Metadata map[string]string `json:"metadata"`
	Links    struct {
		Checkout *struct {
			Href string `json:"href"`
		} `json:"checkout"`
	} `json:"_links"`
}

func (p molliePayment) info() (PaymentInfo, error) {
	cents, err := ParseAmount(p.Amount.Value)
	if err != nil {
		return PaymentInfo{}, err
	}
	info := PaymentInfo{ID: p.ID, Status: p.Status, AmountCents: cents, Currency: p.Amount.Currency, Metadata: p.Metadata}
	if p.Links.Checkout != nil {
		info.CheckoutURL = p.Links.Checkout.Href
	}
	return info, nil
}

// CreatePayment implémente PaymentProvider.CreatePayment. Une webhookUrl
// locale (localhost) est omise : Mollie la refuserait, car il ne peut pas la
// joindre ; le statut est alors relu au retour du client.
func (c *MollieClient) CreatePayment(ctx context.Context, req PaymentRequest) (PaymentInfo, error) {
	payload := map[string]any{
		"amount":      mollieAmount{Currency: req.Currency, Value: FormatAmount(req.AmountCents)},
		"description": req.Description,
		"redirectUrl": req.RedirectURL,
		"metadata":    req.Metadata,
	}
	if req.WebhookURL != "" && !isLocalURL(req.WebhookURL) {
		payload["webhookUrl"] = req.WebhookURL
	}
	var p molliePayment
	if err := c.do(ctx, http.MethodPost, "/payments", payload, &p); err != nil {
		return PaymentInfo{}, err
	}
	return p.info()
}

// GetPayment implémente PaymentProvider.GetPayment.
func (c *MollieClient) GetPayment(ctx context.Context, id string) (PaymentInfo, error) {
	var p molliePayment
	if err := c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(id), nil, &p); err != nil {
		return PaymentInfo{}, err
	}
	return p.info()
}

func (c *MollieClient) do(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mollie: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("mollie: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("mollie: %s %s: %d %s %s", method, path, resp.StatusCode, e.Title, e.Detail)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("mollie: decode response: %w", err)
	}
	return nil
}

// isLocalURL indique si l'URL désigne la machine locale.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

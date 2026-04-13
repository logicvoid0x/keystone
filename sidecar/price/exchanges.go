package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Endpoints of the real exchange adapters. The response formats are covered by
// fixture tests; the live endpoints are not exercised by the test suite.
const (
	CoinbaseURL = "https://api.coinbase.com/v2/prices/BTC-USD/spot"
	KrakenURL   = "https://api.kraken.com/0/public/Ticker?pair=XBTUSD"
	BitstampURL = "https://www.bitstamp.net/api/v2/ticker/btcusd/"
)

type httpSource struct {
	name   string
	url    string
	client *http.Client
	now    func() time.Time
	parse  func(body []byte, fetched time.Time) (Quote, error)
}

func (s *httpSource) Name() string { return s.name }

func (s *httpSource) Fetch(ctx context.Context) (Quote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return Quote{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("%s: HTTP %d", s.name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return Quote{}, err
	}
	return s.parse(body, s.now())
}

// NewMockSource reads a mockfeed endpoint: {"price":"100000.00","ts":<unix>}.
func NewMockSource(name, url string, client *http.Client, now func() time.Time) Source {
	return &httpSource{name: name, url: url, client: client, now: now, parse: parseMock}
}

func parseMock(body []byte, _ time.Time) (Quote, error) {
	var r struct {
		Price string `json:"price"`
		TS    int64  `json:"ts"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Quote{}, err
	}
	p, err := ParseUSD(r.Price)
	if err != nil {
		return Quote{}, err
	}
	return Quote{PriceMicroUSD: p, Time: time.Unix(r.TS, 0)}, nil
}

// NewCoinbase returns the Coinbase spot-price adapter.
func NewCoinbase(client *http.Client, now func() time.Time) Source {
	return &httpSource{name: "coinbase", url: CoinbaseURL, client: client, now: now, parse: parseCoinbase}
}

func parseCoinbase(body []byte, fetched time.Time) (Quote, error) {
	var r struct {
		Data struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Quote{}, err
	}
	if r.Data.Currency != "" && r.Data.Currency != "USD" {
		return Quote{}, fmt.Errorf("coinbase: currency %q", r.Data.Currency)
	}
	p, err := ParseUSD(r.Data.Amount)
	if err != nil {
		return Quote{}, err
	}
	return Quote{PriceMicroUSD: p, Time: fetched}, nil
}

// NewKraken returns the Kraken ticker adapter (last trade price).
func NewKraken(client *http.Client, now func() time.Time) Source {
	return &httpSource{name: "kraken", url: KrakenURL, client: client, now: now, parse: parseKraken}
}

func parseKraken(body []byte, fetched time.Time) (Quote, error) {
	var r struct {
		Error  []string `json:"error"`
		Result map[string]struct {
			C []string `json:"c"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Quote{}, err
	}
	if len(r.Error) > 0 {
		return Quote{}, fmt.Errorf("kraken: %v", r.Error)
	}
	if len(r.Result) != 1 {
		return Quote{}, fmt.Errorf("kraken: %d result pairs", len(r.Result))
	}
	for _, t := range r.Result { // exactly one entry
		if len(t.C) == 0 {
			return Quote{}, errors.New("kraken: no last trade")
		}
		p, err := ParseUSD(t.C[0])
		if err != nil {
			return Quote{}, err
		}
		return Quote{PriceMicroUSD: p, Time: fetched}, nil
	}
	return Quote{}, errors.New("kraken: empty result")
}

// NewBitstamp returns the Bitstamp ticker adapter.
func NewBitstamp(client *http.Client, now func() time.Time) Source {
	return &httpSource{name: "bitstamp", url: BitstampURL, client: client, now: now, parse: parseBitstamp}
}

func parseBitstamp(body []byte, fetched time.Time) (Quote, error) {
	var r struct {
		Last      string `json:"last"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Quote{}, err
	}
	p, err := ParseUSD(r.Last)
	if err != nil {
		return Quote{}, err
	}
	t := fetched
	if ts, err := strconv.ParseInt(r.Timestamp, 10, 64); err == nil && ts > 0 {
		t = time.Unix(ts, 0)
	}
	return Quote{PriceMicroUSD: p, Time: t}, nil
}

package price_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/OWNER/keystone/app/protocol/vetypes"
	"github.com/OWNER/keystone/sidecar/price"
	"github.com/OWNER/keystone/sidecar/price/mockfeed"
)

func TestParseUSD(t *testing.T) {
	ok := map[string]uint64{
		"100000.00":  100_000_000_000,
		"100000":     100_000_000_000,
		"76000.5":    76_000_500_000,
		"1.1234567":  1_123_456, // truncated past 6 decimals
		"0.000001":   1,
		" 65432.10 ": 65_432_100_000,
	}
	for s, want := range ok {
		got, err := price.ParseUSD(s)
		require.NoError(t, err, s)
		require.Equal(t, want, got, s)
	}
	for _, s := range []string{"", "abc", "-1", "1e5", "0", "0.0000001", "99999999999999999999"} {
		_, err := price.ParseUSD(s)
		require.Error(t, err, s)
	}
	require.Equal(t, "100000.00", price.FormatUSD(100_000_000_000))
	require.Equal(t, "77378.09", price.FormatUSD(77_378_093_750))
}

func TestExchangeParsers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name string
		mk   func(c *http.Client, now func() time.Time) price.Source
		body string
		want uint64
		ts   time.Time
	}{
		{"coinbase", price.NewCoinbase, `{"data":{"amount":"67123.45","base":"BTC","currency":"USD"}}`, 67_123_450_000, now},
		{"kraken", price.NewKraken, `{"error":[],"result":{"XXBTZUSD":{"a":["1","1","1"],"c":["67100.10000","0.1"]}}}`, 67_100_100_000, now},
		{"bitstamp", price.NewBitstamp, `{"last":"67150","timestamp":"1699999990"}`, 67_150_000_000, time.Unix(1_699_999_990, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			src := tc.mk(redirectClient(srv.URL), func() time.Time { return now })
			q, err := src.Fetch(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.want, q.PriceMicroUSD)
			require.Equal(t, tc.ts, q.Time)
		})
	}
}

// redirectClient sends every request to target, whatever the URL.
func redirectClient(target string) *http.Client {
	return &http.Client{Transport: rewrite{target}}
}

type rewrite struct{ target string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	t, _ := http.NewRequest(req.Method, r.target, nil)
	u.Scheme, u.Host = t.URL.Scheme, t.URL.Host
	req = req.Clone(req.Context())
	req.URL = &u
	return http.DefaultTransport.RoundTrip(req)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func feed(t *testing.T, usd string, clk *clock) (*mockfeed.Server, *httptest.Server) {
	t.Helper()
	m, err := mockfeed.New(usd, clk.Now)
	require.NoError(t, err)
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	return m, srv
}

func post(t *testing.T, url string, body any) {
	t.Helper()
	bz, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(bz))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// testClient avoids keep-alive connections so httptest.Server.Close never waits on them.
var testClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func mockFetcher(srvURL string, clk *clock) *price.Fetcher {
	var srcs []price.Source
	for _, ex := range mockfeed.Exchanges {
		srcs = append(srcs, price.NewMockSource(ex, srvURL+"/ex/"+ex+"/btcusd", testClient, clk.Now))
	}
	// Generous timeout: the whole suite runs in parallel on loaded CI machines.
	return price.NewFetcher(price.FetcherConfig{Sources: srcs, Now: clk.Now, RequestTimeout: 10 * time.Second})
}

func TestFetcherMedianAndMinSources(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	_, srv := feed(t, "100000", clk)
	f := mockFetcher(srv.URL, clk)

	_, ok := f.Snapshot()
	require.False(t, ok, "no snapshot before the first poll")

	f.Poll(context.Background())
	p, ok := f.Snapshot()
	require.True(t, ok)
	require.Equal(t, uint64(100_000_000_000), p.PriceMicroUsd)
	require.Equal(t, uint32(3), p.NumSources)

	// Skew a and c: the median is b's price.
	post(t, srv.URL+"/admin/offset", map[string]any{"exchange": "a", "bps": 300})
	post(t, srv.URL+"/admin/offset", map[string]any{"exchange": "c", "bps": -200})
	f.Poll(context.Background())
	p, _ = f.Snapshot()
	require.Equal(t, uint64(100_000_000_000), p.PriceMicroUsd)

	// One exchange down: its last quote is still fresh (< 10 s), so 3 sources remain.
	post(t, srv.URL+"/admin/down", map[string]any{"exchange": "a"})
	clk.Add(5 * time.Second)
	f.Poll(context.Background())
	p, ok = f.Snapshot()
	require.True(t, ok)
	require.Equal(t, uint32(3), p.NumSources)

	// After 10 s its quote expires: 2 sources < MinSources → no snapshot.
	clk.Add(6 * time.Second)
	f.Poll(context.Background())
	_, ok = f.Snapshot()
	require.False(t, ok)

	// Back up: snapshot returns.
	post(t, srv.URL+"/admin/up", map[string]any{"exchange": "a"})
	f.Poll(context.Background())
	_, ok = f.Snapshot()
	require.True(t, ok)
}

func TestFetcherSnapshotExpires(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	_, srv := feed(t, "100000", clk)
	f := mockFetcher(srv.URL, clk)
	f.Poll(context.Background())
	_, ok := f.Snapshot()
	require.True(t, ok)
	clk.Add(11 * time.Second) // fetcher stalled: no polls
	_, ok = f.Snapshot()
	require.False(t, ok, "a stalled fetcher must not report an old price")
}

func TestFetcherStartStop(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	_, srv := feed(t, "100000", clk)
	f := mockFetcher(srv.URL, clk)
	f.Start(context.Background())
	require.Eventually(t, func() bool { _, ok := f.Snapshot(); return ok }, 30*time.Second, 50*time.Millisecond)
	f.Stop()
	f.Stop() // idempotent
}

func TestMockfeedAdmin(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	m, srv := feed(t, "100000", clk)

	get := func(ex string) (int, map[string]any) {
		resp, err := http.Get(srv.URL + "/ex/" + ex + "/btcusd")
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, body := get("a")
	require.Equal(t, 200, code)
	require.Equal(t, "100000.00", body["price"])
	require.EqualValues(t, 1_700_000_000, body["ts"])

	post(t, srv.URL+"/admin/price", map[string]any{"usd": "76000"})
	_, body = get("b")
	require.Equal(t, "76000.00", body["price"])

	post(t, srv.URL+"/admin/offset", map[string]any{"exchange": "a", "bps": 30})
	require.Equal(t, uint64(76_228_000_000), m.PriceMicro("a"))

	post(t, srv.URL+"/admin/down", map[string]any{"exchange": "*"})
	for _, ex := range mockfeed.Exchanges {
		code, _ = get(ex)
		require.Equal(t, http.StatusServiceUnavailable, code)
	}
	post(t, srv.URL+"/admin/up", map[string]any{"exchange": "*"})
	code, _ = get("c")
	require.Equal(t, 200, code)

	code, _ = get("z")
	require.Equal(t, http.StatusNotFound, code)
}

type opts map[string]any

func (o opts) Get(k string) any { return o[k] }

func TestFetcherFromAppOptions(t *testing.T) {
	f, err := price.FetcherFromAppOptions(opts{})
	require.NoError(t, err)
	require.Nil(t, f, "no sources configured")

	c := price.MockConfig("http://127.0.0.1:18080/")
	require.Equal(t, "mock:http://127.0.0.1:18080/ex/a/btcusd", c.Sources[0])
	f, err = price.FetcherFromAppOptions(opts{
		price.FlagSources: c.Sources, price.FlagPollInterval: "2s", price.FlagMaxAge: "10s", price.FlagMinSources: 3,
	})
	require.NoError(t, err)
	require.NotNil(t, f)

	f, err = price.FetcherFromAppOptions(opts{price.FlagSources: []string{"coinbase", "kraken", "bitstamp"}})
	require.NoError(t, err)
	require.NotNil(t, f)

	_, err = price.FetcherFromAppOptions(opts{price.FlagSources: []string{"nasdaq"}})
	require.Error(t, err)
	_, err = price.FetcherFromAppOptions(opts{price.FlagSources: []string{"coinbase"}, price.FlagMaxAge: "soon"})
	require.Error(t, err)
}

type slowSource struct{ delay time.Duration }

func (s slowSource) Snapshot() (*vetypes.OraclePayload, bool) {
	time.Sleep(s.delay)
	return &vetypes.OraclePayload{PriceMicroUsd: 1, NumSources: 3}, true
}

func TestWithDeadline(t *testing.T) {
	fast := price.WithDeadline(slowSource{0}, time.Second)
	p, ok := fast.Snapshot()
	require.True(t, ok)
	require.Equal(t, uint64(1), p.PriceMicroUsd)

	slow := price.WithDeadline(slowSource{2 * time.Second}, 50*time.Millisecond)
	start := time.Now()
	_, ok = slow.Snapshot()
	require.False(t, ok, "a late snapshot yields no payload")
	require.Less(t, time.Since(start), time.Second, "Snapshot returns at the deadline")
	slow.Stop() // inner has no Stop: no-op
}

func TestMockfeedPerCallerOffset(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	_, srv := feed(t, "100000", clk)
	resp, err := http.Get(srv.URL + "/ex/a/btcusd?offset_bps=-15")
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.Equal(t, "99850.00", out["price"])
	resp, err = http.Get(srv.URL + "/ex/a/btcusd?offset_bps=x")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

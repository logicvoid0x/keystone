package price

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cast"
)

// app.toml keys under [keystone.price].
const (
	FlagSources      = "keystone.price.sources"
	FlagPollInterval = "keystone.price.poll-interval"
	FlagMaxAge       = "keystone.price.max-age"
	FlagMinSources   = "keystone.price.min-sources"

	mockPrefix = "mock:"
)

// Config is the [keystone.price] section of app.toml.
type Config struct {
	Sources      []string `mapstructure:"sources"`
	PollInterval string   `mapstructure:"poll-interval"`
	MaxAge       string   `mapstructure:"max-age"`
	MinSources   int      `mapstructure:"min-sources"`
}

// DefaultConfig uses the three real exchanges.
func DefaultConfig() Config {
	return Config{
		Sources:      []string{"coinbase", "kraken", "bitstamp"},
		PollInterval: DefaultPollInterval.String(),
		MaxAge:       DefaultMaxAge.String(),
		MinSources:   DefaultMinSources,
	}
}

// MockConfig points the three sources at a mockfeed (exchanges a, b, c).
func MockConfig(baseURL string) Config {
	c := DefaultConfig()
	base := strings.TrimRight(baseURL, "/")
	c.Sources = []string{mockPrefix + base + "/ex/a/btcusd", mockPrefix + base + "/ex/b/btcusd", mockPrefix + base + "/ex/c/btcusd"}
	return c
}

// ConfigTemplate is appended to the SDK app.toml template.
const ConfigTemplate = `
###############################################################################
###                         Keystone price sidecar                          ###
###############################################################################

[keystone.price]

# Price sources: "coinbase", "kraken", "bitstamp", or "mock:<url>" for a mockfeed endpoint.
sources = [{{ range .Keystone.Price.Sources }}"{{ . }}", {{ end }}]
# How often each source is polled.
poll-interval = "{{ .Keystone.Price.PollInterval }}"
# Quotes older than this are dropped.
max-age = "{{ .Keystone.Price.MaxAge }}"
# Fewer fresh quotes than this means no oracle payload.
min-sources = {{ .Keystone.Price.MinSources }}
`

// AppOptions is the subset of servertypes.AppOptions used here.
type AppOptions interface{ Get(string) any }

// FetcherFromAppOptions builds a Fetcher from app.toml. It returns nil, nil when no
// sources are configured.
func FetcherFromAppOptions(opts AppOptions) (*Fetcher, error) {
	names := cast.ToStringSlice(opts.Get(FlagSources))
	if len(names) == 0 {
		return nil, nil
	}
	poll, err := durationOr(opts.Get(FlagPollInterval), DefaultPollInterval)
	if err != nil {
		return nil, err
	}
	maxAge, err := durationOr(opts.Get(FlagMaxAge), DefaultMaxAge)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: requestTimeout}
	var sources []Source
	for i, n := range names {
		switch {
		case n == "coinbase":
			sources = append(sources, NewCoinbase(client, time.Now))
		case n == "kraken":
			sources = append(sources, NewKraken(client, time.Now))
		case n == "bitstamp":
			sources = append(sources, NewBitstamp(client, time.Now))
		case strings.HasPrefix(n, mockPrefix):
			sources = append(sources, NewMockSource(fmt.Sprintf("mock%d", i), strings.TrimPrefix(n, mockPrefix), client, time.Now))
		default:
			return nil, fmt.Errorf("unknown price source %q", n)
		}
	}
	return NewFetcher(FetcherConfig{
		Sources: sources, PollInterval: poll, MaxAge: maxAge, MinSources: cast.ToInt(opts.Get(FlagMinSources)),
	}), nil
}

func durationOr(v any, def time.Duration) (time.Duration, error) {
	s := cast.ToString(v)
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

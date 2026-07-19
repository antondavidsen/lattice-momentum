package provider

import (
	"fmt"

	"ai-stock-service/internal/config"
)

// NewFromConfig instantiates the correct Provider implementation based on
// cfg.MarketDataProvider ("polygon" or "twelvedata").
// Returns an error when the provider name is unrecognised or the required API
// key is missing.
func NewFromConfig(cfg *config.Config) (Provider, error) {
	switch cfg.MarketDataProvider {
	case "polygon":
		if cfg.PolygonAPIKey == "" {
			return nil, fmt.Errorf("POLYGON_API_KEY is required when MARKET_DATA_PROVIDER=polygon")
		}
		return NewPolygon(PolygonConfig{
			APIKey:         cfg.PolygonAPIKey,
			RequestsPerMin: cfg.PolygonRequestsPerMin,
		}), nil

	case "twelvedata":
		if cfg.TwelveDataAPIKey == "" {
			return nil, fmt.Errorf("TWELVEDATA_API_KEY is required when MARKET_DATA_PROVIDER=twelvedata")
		}
		return NewTwelveData(TwelveDataConfig{
			APIKey:         cfg.TwelveDataAPIKey,
			RequestsPerMin: cfg.TwelveDataRequestsPerMin,
		}), nil

	default:
		return nil, fmt.Errorf("unsupported MARKET_DATA_PROVIDER %q (supported: polygon, twelvedata)", cfg.MarketDataProvider)
	}
}

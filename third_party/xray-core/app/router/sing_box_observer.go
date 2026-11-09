package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

// BindSingBoxObserver reuses the pinned balancing algorithms without a second runtime.
func BindSingBoxObserver(strategy BalancingStrategy, ctx context.Context, observer extension.Observatory) error {
	switch s := strategy.(type) {
	case *RandomStrategy:
		s.ctx = ctx
		if s.FallbackTag != "" {
			s.observatory = observer
		}
	case *RoundRobinStrategy:
		s.ctx = ctx
		if s.FallbackTag != "" {
			s.observatory = observer
		}
	case *LeastPingStrategy:
		s.ctx = ctx
		s.observatory = observer
	case *LeastLoadStrategy:
		s.ctx = ctx
		s.observer = observer
	default:
		return fmt.Errorf("unsupported balancing strategy %T", strategy)
	}
	return nil
}

// ParseSingBoxLeastLoadSettings uses the same normalization as the JSON configuration builder.
func ParseSingBoxLeastLoadSettings(raw []byte) (*StrategyLeastLoadConfig, error) {
	var input struct {
		Costs     []*StrategyWeight   `json:"costs"`
		Baselines []duration.Duration `json:"baselines"`
		Expected  int32               `json:"expected"`
		MaxRTT    duration.Duration   `json:"maxRTT"`
		Tolerance float64             `json:"tolerance"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	settings := &StrategyLeastLoadConfig{Costs: input.Costs, Expected: input.Expected, MaxRTT: int64(input.MaxRTT), Tolerance: float32(input.Tolerance)}
	if settings.Expected < 0 {
		settings.Expected = 0
	}
	if settings.MaxRTT < 0 {
		settings.MaxRTT = 0
	}
	if settings.Tolerance < 0 {
		settings.Tolerance = 0
	}
	if settings.Tolerance > 1 {
		settings.Tolerance = 1
	}
	for _, b := range input.Baselines {
		if b > 0 {
			settings.Baselines = append(settings.Baselines, int64(b))
		}
	}
	return settings, nil
}

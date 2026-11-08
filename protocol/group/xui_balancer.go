package group

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand"
	"net"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

type probeSample struct {
	delay  time.Duration
	failed bool
}

type balanceReport struct {
	alive    bool
	delay    time.Duration
	score    float64
	total    int
	failures int
	updated  time.Time
}

type balanceCost struct {
	match  string
	regexp *regexp.Regexp
	value  float64
}

type balanceState struct {
	leastExpected  int
	leastMaxRTT    time.Duration
	leastTolerance float64
	leastBaselines []time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	config         option.XrayBalancerOptions
	history        *urltest.HistoryStorage
	mu             sync.RWMutex
	wg             sync.WaitGroup
	closed         bool
	started        bool
	next           uint64
	costs          []balanceCost
	reports        map[string]balanceReport
	samples        map[string][]probeSample
	targets        map[string]adapter.Outbound
}

func newBalanceState(ctx context.Context, options option.XrayBalancerOptions) (*balanceState, error) {
	options.Strategy = strings.ToLower(strings.TrimSpace(options.Strategy))
	if options.Strategy == "" {
		options.Strategy = "random"
	}
	switch options.Strategy {
	case "random", "roundrobin", "leastping", "leastload":
	default:
		return nil, fmt.Errorf("unsupported balancer strategy: %s", options.Strategy)
	}
	if options.Interval <= 0 {
		options.Interval = badoption.Duration(300 * time.Second)
	}
	if options.Timeout <= 0 {
		options.Timeout = badoption.Duration(10 * time.Second)
	}
	if options.Sampling == 0 {
		options.Sampling = 5
	}
	if options.Sampling > 64 || options.Interval.Build() < time.Second || options.Timeout.Build() < time.Millisecond {
		return nil, fmt.Errorf("invalid balancing probe limits")
	}
	for _, link := range []string{options.ProbeURL, options.Connectivity} {
		if link == "" {
			continue
		}
		u, err := url.Parse(link)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid balancing probe URL: %q", link)
		}
	}
	if (options.Strategy == "leastping" || options.Strategy == "leastload" || options.Fallback != "") && options.ProbeURL == "" {
		return nil, fmt.Errorf("health balancing requires a probe_url")
	}
	if options.Method != "" && options.Method != "GET" && options.Method != "HEAD" {
		return nil, fmt.Errorf("unsupported balancer probe method: %q", options.Method)
	}
	var costs []balanceCost
	var leastExpected int
	var leastMaxRTT time.Duration
	var leastTolerance float64
	var leastBaselines []time.Duration
	if options.Strategy == "leastload" && len(options.Settings) > 0 {
		var settings struct {
			Baselines []string `json:"baselines"`
			Expected  int      `json:"expected"`
			MaxRTT    string   `json:"maxRTT"`
			Tolerance float64  `json:"tolerance"`
			Costs []*struct {
				Match  string  `json:"match"`
				Regexp bool   `json:"regexp"`
				Value  float64 `json:"value"`
			} `json:"costs"`
		}
		decoder := json.NewDecoder(bytes.NewReader(options.Settings))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&settings); err != nil {
			return nil, fmt.Errorf("invalid leastLoad settings: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("invalid leastLoad settings: trailing data")
		}
		leastExpected = max(settings.Expected, 0)
		leastTolerance = min(max(settings.Tolerance, 0), 1)
		if settings.MaxRTT != "" {
			value, err := time.ParseDuration(settings.MaxRTT)
			if err != nil {
				return nil, fmt.Errorf("invalid leastLoad maxRTT: %w", err)
			}
			leastMaxRTT = max(value, 0)
		}
		for _, baseline := range settings.Baselines {
			value, err := time.ParseDuration(baseline)
			if err != nil {
				return nil, fmt.Errorf("invalid leastLoad baseline: %w", err)
			}
			if value > 0 {
				leastBaselines = append(leastBaselines, value)
			}
		}
		for _, cost := range settings.Costs {
			if cost == nil {
				return nil, fmt.Errorf("leastLoad costs must be objects")
			}
			item := balanceCost{match: cost.Match, value: cost.Value}
			if cost.Regexp {
				expression, err := regexp.Compile(cost.Match)
				if err != nil {
					return nil, fmt.Errorf("invalid leastLoad cost regex: %w", err)
				}
				item.regexp = expression
			}
			costs = append(costs, item)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &balanceState{
		leastExpected:  leastExpected,
		leastMaxRTT:    leastMaxRTT,
		leastTolerance: leastTolerance,
		leastBaselines: leastBaselines,
		ctx:            ctx,
		cancel:         cancel,
		config:         options,
		history:        service.PtrFromContext[urltest.HistoryStorage](ctx),
		costs:          costs,
		reports:        make(map[string]balanceReport),
		samples:        make(map[string][]probeSample),
	}, nil
}

func (b *balanceState) scoreFor(tag string, delay time.Duration) float64 {
	score := float64(delay)
	for _, cost := range b.costs {
		match := strings.Contains(tag, cost.match) && cost.match != ""
		if cost.regexp != nil {
			match = cost.regexp.MatchString(tag)
		}
		if match {
			weight := cost.value
			if weight <= 0 {
				weight = 1
				value := regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`).FindString(cost.match)
				if value != "" {
					if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed > 0 {
						weight = parsed
					}
				}
			}
			score *= math.Sqrt(weight)
			break
		}
	}
	return score
}

func (b *balanceState) pick(tags []string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var candidates []string
	for _, tag := range tags {
		report, observed := b.reports[tag]
		if b.config.ProbeURL != "" {
			if !observed || !report.alive {
				continue
			}
			if b.config.Strategy == "leastload" {
				if b.leastMaxRTT > 0 && report.delay >= b.leastMaxRTT {
					continue
				}
				if b.leastTolerance > 0 && report.total > 0 && float64(report.failures)/float64(report.total) > b.leastTolerance {
					continue
				}
			}
			if b.config.Burst && time.Since(report.updated) > b.config.Interval.Build()*time.Duration(b.config.Sampling)*2 {
				continue
			}
		}
		candidates = append(candidates, tag)
	}
	if len(candidates) == 0 {
		if b.config.Fallback != "" {
			return b.config.Fallback, nil
		}
		return "", fmt.Errorf("balancer has no healthy outbound")
	}
	switch b.config.Strategy {
	case "roundrobin":
		tag := candidates[b.next%uint64(len(candidates))]
		b.next++
		return tag, nil
	case "random":
		return candidates[rand.Intn(len(candidates))], nil
	case "leastping", "leastload":
		sort.Slice(candidates, func(i, j int) bool {
			left, right := b.reports[candidates[i]], b.reports[candidates[j]]
			if b.config.Strategy == "leastload" {
				if left.score != right.score {
					return left.score < right.score
				}
			} else if left.delay != right.delay {
				return left.delay < right.delay
			}
			return candidates[i] < candidates[j]
		})
		if b.config.Strategy == "leastload" {
			count := min(max(b.leastExpected, 1), len(candidates))
			if len(b.leastBaselines) > 0 {
				count = 0
				for _, baseline := range b.leastBaselines {
					for count < len(candidates) && b.reports[candidates[count]].score < float64(baseline) {
						count++
					}
					if count >= max(b.leastExpected, 1) {
						break
					}
				}
				if b.leastExpected > 0 {
					count = max(count, min(b.leastExpected, len(candidates)))
				}
			}
			if count == 0 {
				if b.config.Fallback != "" {
					return b.config.Fallback, nil
				}
				return "", fmt.Errorf("leastLoad has no qualifying outbound")
			}
			return candidates[rand.Intn(count)], nil
		}
		return candidates[0], nil
	}
	return "", fmt.Errorf("unsupported balancer strategy")
}

func (b *balanceState) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.cancel()
	b.mu.Unlock()
	b.wg.Wait()
	return nil
}

func (b *balanceState) postStart(targets map[string]adapter.Outbound) {
	b.mu.Lock()
	if b.started || b.closed {
		b.mu.Unlock()
		return
	}
	b.started = true
	b.targets = make(map[string]adapter.Outbound, len(targets))
	maps.Copy(b.targets, targets)
	if b.config.ProbeURL == "" {
		b.mu.Unlock()
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		b.probe()
		ticker := time.NewTicker(b.config.Interval.Build())
		defer ticker.Stop()
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-ticker.C:
				b.probe()
			}
		}
	}()
}

func (b *balanceState) probe() {
	b.mu.RLock()
	targets := make(map[string]adapter.Outbound, len(b.targets))
	maps.Copy(targets, b.targets)
	b.mu.RUnlock()
	limit := 1
	if b.config.Concurrent {
		limit = 4
	}
	semaphore := make(chan struct{}, limit)
	var group sync.WaitGroup
	for tag, detour := range targets {
		if tag == b.config.Fallback {
			continue
		}
		if len(b.config.ProbeTags) > 0 && !slices.Contains(b.config.ProbeTags, tag) {
			continue
		}
		select {
		case semaphore <- struct{}{}:
		case <-b.ctx.Done():
			group.Wait()
			return
		}
		group.Add(1)
		go func(tag string, detour adapter.Outbound) {
			defer group.Done()
			defer func() { <-semaphore }()
			ctx, cancel := context.WithTimeout(b.ctx, b.config.Timeout.Build())
			delay, err := urltest.URLTestWithMethod(ctx, b.config.ProbeURL, b.config.Method, detour)
			cancel()
			if err != nil && b.config.Connectivity != "" {
				ctx2, cancel2 := context.WithTimeout(b.ctx, b.config.Timeout.Build())
				_, connErr := urltest.URLTestWithMethod(ctx2, b.config.Connectivity, b.config.Method, balanceDirectDialer{})
				cancel2()
				if connErr != nil {
					return
				}
			}
			b.record(tag, time.Duration(delay)*time.Millisecond, err)
		}(tag, detour)
	}
	group.Wait()
}

type balanceDirectDialer struct{}

func (balanceDirectDialer) DialContext(ctx context.Context, network string, address M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address.String())
}

func (balanceDirectDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("packet probing is unsupported")
}

func (b *balanceState) record(tag string, delay time.Duration, err error) {
	b.mu.Lock()
	samples := append(b.samples[tag], probeSample{delay: delay, failed: err != nil})
	if len(samples) > int(b.config.Sampling) {
		samples = samples[len(samples)-int(b.config.Sampling):]
	}
	b.samples[tag] = samples
	alive := err == nil
	score := b.scoreFor(tag, delay)
	failures := 0
	for _, sample := range samples {
		if sample.failed {
			failures++
		}
	}
	if b.config.Burst {
		alive = failures < len(samples)
		var good int
		var sum time.Duration
		for _, sample := range samples {
			if !sample.failed {
				good++
				sum += sample.delay
			}
		}
		if good > 0 {
			delay = sum / time.Duration(good)
			var variance float64
			for _, sample := range samples {
				if !sample.failed {
					diff := float64(sample.delay - delay)
					variance += diff * diff
				}
			}
			deviation := float64(delay) / 2
			if good > 1 {
				deviation = math.Sqrt(variance / float64(good-1))
			}
			score = b.scoreFor(tag, time.Duration(deviation))
		}
	}
	b.reports[tag] = balanceReport{alive: alive, delay: delay, score: score, total: len(samples), failures: failures, updated: time.Now()}
	b.mu.Unlock()
	if b.history != nil {
		measured := uint16(min(max(delay.Milliseconds(), 0), 65535))
		if !alive {
			measured = 65535
		}
		b.history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: measured})
	}
}

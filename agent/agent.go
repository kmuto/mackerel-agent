package agent

import (
	"context"
	"errors"
	"time"

	"github.com/agatan/timejump"
	"github.com/mackerelio/mackerel-agent/checks"
	"github.com/mackerelio/mackerel-agent/config"
	"github.com/mackerelio/mackerel-agent/mackerel"
	"github.com/mackerelio/mackerel-agent/metadata"
	"github.com/mackerelio/mackerel-agent/metrics"
	mkr "github.com/mackerelio/mackerel-client-go"
)

// Agent is the root of metrics collectors
type Agent struct {
	MetricsGenerators  []metrics.Generator
	PluginGenerators   []metrics.PluginGenerator
	Checkers           []*checks.Checker
	MetadataGenerators []*metadata.Generator
}

// MetricsResult XXX
type MetricsResult struct {
	Created time.Time
	Values  []*metrics.ValuesCustomIdentifier
}

func (agent *Agent) CollectMetrics_mock(collectedTime time.Time) *MetricsResult {
	return &MetricsResult{Created: collectedTime, Values: nil}
}

// CollectMetrics collects metrics with generators.
func (agent *Agent) CollectMetrics(collectedTime time.Time) *MetricsResult {
	generators := agent.MetricsGenerators
	for _, g := range agent.PluginGenerators {
		generators = append(generators, g)
	}
	values := generateValues(generators)
	return &MetricsResult{Created: collectedTime, Values: values}
}

func ticktuck(result chan time.Time, from time.Time, to time.Time, done chan struct{}) {
	from_t := from.Unix()
	to_t := to.Unix()
	for t := from_t; t < to_t; t++ {
		result <- time.Unix(t, 0)
	}
	done <- struct{}{}
}

func (agent *Agent) Watch_mock(conf *config.Config, ctx context.Context, commandTicker chan time.Time, done chan struct{}) (chan *MetricsResult, error) {
	metricsResult := make(chan *MetricsResult)
	ticker := make(chan time.Time)
	interval := config.PostMetricsInterval

	from, err := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimFrom)
	if err != nil {
		logger.Errorf("from format error %v", err)
		return nil, errors.New("time format error")
	}
	to, err := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimTo)
	if err != nil {
		logger.Errorf("to format error %v", err)
		return nil, errors.New("time format error")
	}

	go func() {
		t := make(chan time.Time, 1)
		go ticktuck(t, from, to, done)

		last := from
		ticker <- last // sends tick once at first

		for {
			select {
			case <-ctx.Done():
				close(ticker)
				close(t)
				return
			case <-done:
				close(ticker)
				close(t)
				return
			case ti := <-t:
				commandTicker <- ti
				if ti.Second()%int(interval.Seconds()) == 0 || ti.After(last.Add(interval)) {
					select {
					case ticker <- ti:
						last = ti
					default:
					}
				}
			}
		}
	}()

	const collectMetricsWorkerMax = 3

	go func(ticker chan time.Time) {
		// Start collectMetrics concurrently
		// so that it does not prevent running next collectMetrics.
		sem := make(chan struct{}, collectMetricsWorkerMax)
		for tickedTime := range ticker {
			ti := tickedTime
			sem <- struct{}{}
			go func() {
				metricsResult <- agent.CollectMetrics_mock(ti)
				<-sem
			}()
		}
	}(ticker)

	return metricsResult, nil
}

func (agent *Agent) FromTo(conf *config.Config) (time.Time, time.Time) {
	// 雑にエラーは握り潰している
	from, _ := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimFrom)
	to, _ := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimTo)
	return from, to
}

// Watch XXX
func (agent *Agent) Watch_clockup(conf *config.Config, ctx context.Context, done chan struct{}) (chan *MetricsResult, error) {
	metricsResult := make(chan *MetricsResult)
	ticker := make(chan time.Time)
	// interval := config.PostMetricsInterval

	from, to := agent.FromTo(conf)

	go func() {
		// FIXME:開始時刻をcommandと合わせるには？
		timejump.Activate()
		defer timejump.Deactivate()
		timejump.Scale(1000)
		timejump.Jump(from)

		t := time.NewTicker(1 * time.Millisecond) // 1 second->millisecond
		last := timejump.Now()
		ticker <- last // sends tick once at first

		for {
			select {
			case <-ctx.Done():
				close(ticker)
				t.Stop()
				return
			case <-t.C:
				// t.Cの値は現在時刻ベース & ミリ秒カウンタなので見ても意味がない。timejump.Now()で動いている時間を使う
				// Fire an event at 0 second per minute.
				// Because ticks may not be accurate,
				// fire an event if t - last is more than 1 minute
				if last.Equal(to) || last.After(to) {
					close(ticker)
					t.Stop()
					done <- struct{}{}
					return
				}
				now := timejump.Now()
				if now.Second()%60 == 0 || now.After(last.Add(60*time.Second)) {
					// Non-blocking send of time.
					// If `collectMetrics` runs with max concurrency, we drop ticks.
					// Because the time is used as agent.MetricsResult.Created.
					select {
					case ticker <- timejump.Now():
						last = timejump.Now()
					default:
					}
				}
			}
		}
	}()

	const collectMetricsWorkerMax = 3

	go func() {
		// Start collectMetrics concurrently
		// so that it does not prevent running next collectMetrics.
		sem := make(chan struct{}, collectMetricsWorkerMax)
		for tickedTime := range ticker {
			ti := tickedTime
			sem <- struct{}{}
			go func() {
				metricsResult <- agent.CollectMetrics(ti)
				<-sem
			}()
		}
	}()

	return metricsResult, nil
}

// Watch XXX
func (agent *Agent) Watch(ctx context.Context) chan *MetricsResult {

	metricsResult := make(chan *MetricsResult)
	ticker := make(chan time.Time)
	interval := config.PostMetricsInterval

	go func() {
		t := time.NewTicker(1 * time.Second)

		last := time.Now()
		ticker <- last // sends tick once at first

		for {
			select {
			case <-ctx.Done():
				close(ticker)
				t.Stop()
				return
			case t := <-t.C:
				// Fire an event at 0 second per minute.
				// Because ticks may not be accurate,
				// fire an event if t - last is more than 1 minute
				if t.Second()%int(interval.Seconds()) == 0 || t.After(last.Add(interval)) {
					// Non-blocking send of time.
					// If `collectMetrics` runs with max concurrency, we drop ticks.
					// Because the time is used as agent.MetricsResult.Created.
					select {
					case ticker <- t:
						last = t
					default:
					}
				}
			}
		}
	}()

	const collectMetricsWorkerMax = 3

	go func() {
		// Start collectMetrics concurrently
		// so that it does not prevent running next collectMetrics.
		sem := make(chan struct{}, collectMetricsWorkerMax)
		for tickedTime := range ticker {
			ti := tickedTime
			sem <- struct{}{}
			go func() {
				metricsResult <- agent.CollectMetrics(ti)
				<-sem
			}()
		}
	}()

	return metricsResult
}

// CollectGraphDefsOfPlugins collects GraphDefs of Plugins
func (agent *Agent) CollectGraphDefsOfPlugins() []*mkr.GraphDefsParam {
	payloads := []*mkr.GraphDefsParam{}

	for _, g := range agent.PluginGenerators {
		p, err := g.PrepareGraphDefs()
		if err != nil {
			logger.Debugf("Failed to fetch meta information from plugin %v (non critical); seems that this plugin does not have meta information: %v", g, err)
		}
		if p != nil {
			payloads = append(payloads, p...)
		}
	}

	return payloads
}

// InitPluginGenerators XXX
func (agent *Agent) InitPluginGenerators(api *mackerel.API) {
	payloads := agent.CollectGraphDefsOfPlugins()

	if len(payloads) > 0 {
		err := api.CreateGraphDefs(payloads)
		if err != nil {
			logger.Errorf("Failed to create graphdefs: %s", err)
		}
	}
}

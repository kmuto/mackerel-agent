package agent

import (
	"context"
	"time"

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

// Watch XXX
func (agent *Agent) Watch(ctx context.Context, commandTicker chan time.Time) chan *MetricsResult {

	metricsResult := make(chan *MetricsResult)
	ticker := make(chan time.Time)
	interval := config.PostMetricsInterval

	go func() {
		from := time.Date(2023, 7, 28, 03, 33, 0, 0, time.Local)
		// .Add(time.Second * 1)
		//to := time.Date(2023, 7, 28, 18, 0, 0, 0, time.Local)
		to := time.Date(2023, 7, 28, 03, 35, 0, 0, time.Local)
		//t := time.NewTicker(1 * time.Second)
		t := make(chan time.Time)
		done := make(chan struct{})
		go ticktuck(t, from, to, done)

		last := from
		ticker <- last // sends tick once at first

		for {
			select {
			case <-ctx.Done():
				close(ticker)
				close(t)
				return
			case ti := <-t:
				commandTicker <- ti
				// Fire an event at 0 second per minute.
				// Because ticks may not be accurate,
				// fire an event if t - last is more than 1 minute
				if ti.Second()%int(interval.Seconds()) == 0 || ti.After(last.Add(interval)) {
					// Non-blocking send of time.
					// If `collectMetrics` runs with max concurrency, we drop ticks.
					// Because the time is used as agent.MetricsResult.Created.
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
				metricsResult <- agent.CollectMetrics(ti)
				<-sem
			}()
		}
	}(ticker)

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

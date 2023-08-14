package agent

import (
	"context"
	"fmt"
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

func (agent *Agent) CollectMetrics_clockup(collectedTime time.Time, isDown bool) *MetricsResult {
	generators := agent.MetricsGenerators
	for _, g := range agent.PluginGenerators {
		generators = append(generators, g)
	}
	values := generateValues(generators)
	// ジェネレータによる取得時間を5秒としてみる
	if isDown {
		// ジェネレータは120秒を超えると警告するようになっているようだ。ただ停止するわけではない
		// ここはあまり関係ないはずなので同じに
		<-time.After(5 * time.Millisecond)
	} else {
		<-time.After(5 * time.Millisecond)
	}
	return &MetricsResult{Created: collectedTime, Values: values}
}

// CollectMetrics collects metrics with generators.
func (agent *Agent) CollectMetrics(collectedTime time.Time) *MetricsResult {
	generators := agent.MetricsGenerators
	for _, g := range agent.PluginGenerators {
		generators = append(generators, g)
	}
	values := generateValues_clockup(generators)
	return &MetricsResult{Created: collectedTime, Values: values}
}

func (agent *Agent) FromTo(conf *config.Config) (time.Time, time.Time) {
	// 雑にエラーは握り潰している
	from, _ := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimFrom)
	to, _ := time.Parse("2006-01-02T15:04:05Z07:00", conf.SimTo)
	return from, to
}

func isDowntime(conf *config.Config, nowTime time.Time) bool {
	for _, Down := range conf.SimDowns {
		from, _ := time.Parse("2006-01-02T15:04:05Z07:00", Down[0])
		to, _ := time.Parse("2006-01-02T15:04:05Z07:00", Down[1])

		nowTime := timejump.Now()
		if (from.Equal(nowTime) || from.Before(nowTime)) && to.After(nowTime) {
			return true
		}
	}
	return false
}

// Watch XXX
func (agent *Agent) Watch_clockup(conf *config.Config, ctx context.Context, done chan struct{}) (chan *MetricsResult, error) {
	metricsResult := make(chan *MetricsResult)
	ticker := make(chan time.Time)
	interval := config.PostMetricsInterval

	from, to := agent.FromTo(conf)

	go func() {
		startTime := time.Now()
		t := time.NewTicker(1 * time.Millisecond) // 1 second->millisecond
		last := timejump.Now()
		ticker <- last // sends tick once at first

		for {
			select {
			case <-ctx.Done():
				close(ticker)
				t.Stop()
				return
			case tick := <-t.C:
				// t.Cの値は現在時刻ベース & ミリ秒カウンタ。1000倍システムの時間にする
				now := from.Add(tick.Sub(startTime) * time.Duration(1000)) // tick値を使う
				// Fire an event at 0 second per minute.
				// Because ticks may not be accurate,
				// fire an event if t - last is more than 1 minute
				if last.Equal(to) || last.After(to) {
					close(ticker)
					t.Stop()
					done <- struct{}{}
					return
				}
				if now.Second()%int(interval.Seconds()) == 0 || now.After(last.Add(interval)) {
					// Non-blocking send of time.
					// If `collectMetrics` runs with max concurrency, we drop ticks.
					// Because the time is used as agent.MetricsResult.Created.
					// 全然tickerが詰まってくれないわけだが
					select {
					case ticker <- now:
						last = now
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
				if timejump.Now().Sub(ti) > time.Duration(1*time.Minute) {
					fmt.Println("K:", timejump.Now().Unix(), ":DELAYED:", ti.Unix())
				}
				metricsResult <- agent.CollectMetrics_clockup(ti, isDowntime(conf, ti))
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

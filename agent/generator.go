package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/mackerelio/golib/logging"
	"github.com/mackerelio/mackerel-agent/metrics"
)

var logger = logging.GetLogger("agent")

type testGenerator2 struct{}

func (g *testGenerator2) Generate() (metrics.Values, error) {
	values := make(map[string]float64)
	values["test"] = 10
	return values, nil
}

func generateValues_mock(generators []metrics.Generator) []*metrics.ValuesCustomIdentifier {
	tg := &testGenerator2{}
	generators2 := []metrics.Generator{tg}
	values := generateValues(generators2)

	result := make(chan []*metrics.ValuesCustomIdentifier)
	// allValues := []*metrics.ValuesCustomIdentifier{}

	result <- values
	return <-result
}

func generateValues(generators []metrics.Generator) []*metrics.ValuesCustomIdentifier {
	fmt.Println("ここにはこない generateValues")
	processed := make(chan *metrics.ValuesCustomIdentifier)
	finish := make(chan struct{})
	result := make(chan []*metrics.ValuesCustomIdentifier)

	go func() {
		allValues := []*metrics.ValuesCustomIdentifier{}
		for {
			select {
			case values := <-processed:
				allValues = metrics.MergeValuesCustomIdentifiers(allValues, values)
			case <-finish:
				result <- allValues
				return
			}
		}
	}()

	go func() {
		var wg sync.WaitGroup
		for _, g := range generators {
			wg.Add(1)
			go func(g metrics.Generator) {
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf("Panic: generating value in %T (skip this metric): %s", g, r)
					}
					wg.Done()
				}()

				startedAt := time.Now()
				values, err := g.Generate()
				if seconds := (time.Since(startedAt) / time.Second); seconds > 120 {
					logger.Warningf("%T.Generate() take a long time (%d seconds)", g, seconds)
				}
				if err != nil {
					logger.Errorf("Failed to generate value in %T (skip this metric): %s", g, err.Error())
					return
				}
				var customIdentifier *string
				if pluginGenerator, ok := g.(metrics.PluginGenerator); ok {
					customIdentifier = pluginGenerator.CustomIdentifier()
				}
				processed <- &metrics.ValuesCustomIdentifier{
					Values:           values,
					CustomIdentifier: customIdentifier,
				}
			}(g)
		}
		wg.Wait()
		finish <- struct{}{} // processed all jobs
	}()

	return <-result
}

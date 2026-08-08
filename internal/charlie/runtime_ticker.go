package charlie

import "time"

type runtimeTicker interface {
	C() <-chan time.Time
	Stop()
}

type realRuntimeTicker struct{ ticker *time.Ticker }

func (t realRuntimeTicker) C() <-chan time.Time { return t.ticker.C }
func (t realRuntimeTicker) Stop()               { t.ticker.Stop() }

func newRuntimeTicker(interval time.Duration) runtimeTicker {
	return realRuntimeTicker{ticker: time.NewTicker(interval)}
}

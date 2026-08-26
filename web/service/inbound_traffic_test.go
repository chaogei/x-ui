package service

import (
	"testing"

	"x-ui/core"
	"x-ui/database/model"
	"x-ui/testutil"
)

// Every worker updates the same SQLite row. The final counters must equal the
// sum of all deltas; a read-modify-write implementation would lose updates.
func TestInboundAddTrafficConcurrentSameTag(t *testing.T) {
	testutil.InitDB(t)
	inbound := seedInbound(t, model.VMess, 30200, `{"users":[]}`)

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	for range workers {
		go func() {
			<-start
			results <- (&InboundService{}).AddTraffic([]*core.Traffic{{
				IsInbound: true,
				Tag:       inbound.Tag,
				Up:        3,
				Down:      5,
			}})
		}()
	}
	close(start)

	for i := 0; i < workers; i++ {
		if err := <-results; err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}

	reloaded, err := (&InboundService{}).GetInbound(inbound.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if reloaded.Up != workers*3 || reloaded.Down != workers*5 {
		t.Errorf("up/down = %d/%d, want %d/%d",
			reloaded.Up, reloaded.Down, workers*3, workers*5)
	}
}

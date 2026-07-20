package syncer

import (
	"testing"
	"time"
)

func TestStopAndDrainTimerDoesNotBlockAfterChannelWasDrained(t *testing.T) {
	timer := time.NewTimer(time.Millisecond)
	<-timer.C
	done := make(chan struct{})
	go func() {
		stopAndDrainTimer(timer)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopping an already-drained timer blocked")
	}
}

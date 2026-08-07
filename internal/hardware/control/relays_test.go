package control

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type relayRecorder struct {
	calls []string
}

func (recorder *relayRecorder) SetRelay1(high bool) error {
	recorder.calls = append(recorder.calls, fmt.Sprintf("relay1=%t", high))
	return nil
}

func (recorder *relayRecorder) SetRelay2(high bool) error {
	recorder.calls = append(recorder.calls, fmt.Sprintf("relay2=%t", high))
	return nil
}

func TestApplyRelaysPreservesDischargeSwitchOrder(t *testing.T) {
	recorder := &relayRecorder{}
	wait := func(_ context.Context, duration time.Duration) error {
		recorder.calls = append(recorder.calls, "wait="+duration.String())
		return nil
	}
	decision := Decision{
		Relay1High: false, Relay2High: true, Relay2First: true,
		SwitchDelay: 50 * time.Millisecond,
	}
	if err := ApplyRelays(context.Background(), recorder, decision, wait); err != nil {
		t.Fatal(err)
	}
	want := []string{"relay2=true", "wait=50ms", "relay1=false"}
	if fmt.Sprint(recorder.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", recorder.calls, want)
	}
}

func TestApplyRelaysPreservesACSwitchOrder(t *testing.T) {
	recorder := &relayRecorder{}
	decision := Decision{Relay1High: true, Relay2High: false}
	if err := ApplyRelays(context.Background(), recorder, decision, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"relay1=true", "relay2=false"}
	if fmt.Sprint(recorder.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", recorder.calls, want)
	}
}

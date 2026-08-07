package control

import (
	"math"
	"testing"
	"time"

	"ups-pi-node/internal/hardware/ina3221"
)

func testController() *Controller {
	return New(Config{
		BatteryEmptyVoltage:     9.3,
		BatteryFullVoltage:      12.6,
		CurrentCutoffMA:         150,
		CurrentNoiseFloorMA:     30,
		CurrentPolarity:         1,
		OutputCurrentPolarity:   1,
		LowVoltageShutdownAfter: 20 * time.Second,
		SwitchDelay:             50 * time.Millisecond,
	})
}

func TestChargingRelayPolicyAndSnapshot(t *testing.T) {
	decision := testController().Step(Input{
		ACOK:    true,
		Battery: ina3221.Measurement{VoltageV: 12.0, CurrentMA: 149.333},
		Output:  ina3221.Measurement{VoltageV: 19.0, CurrentMA: 554.667},
		Elapsed: time.Second, ACSensorPin: 17, OutputChannel: 2,
	})

	if !decision.Relay1High || decision.Relay2High || decision.Relay2First {
		t.Fatalf("unexpected charging relay decision: %#v", decision)
	}
	if decision.Snapshot.State != "CHARGE" || decision.Snapshot.Color != "yellow" {
		t.Fatalf("unexpected snapshot: %#v", decision.Snapshot)
	}
	if math.Abs(decision.Snapshot.OutputPowerW-19.0*554.667/1000) > 1e-9 {
		t.Fatalf("output power = %f", decision.Snapshot.OutputPowerW)
	}
}

func TestChargedLatchMatchesPythonThresholds(t *testing.T) {
	controller := testController()
	charged := controller.Step(Input{
		ACOK:    true,
		Battery: ina3221.Measurement{VoltageV: 12.56, CurrentMA: 100},
	})
	if !charged.Relay2High || charged.Snapshot.State != "CHARGED" {
		t.Fatalf("battery did not enter CHARGED: %#v", charged)
	}

	// Python reports one final CHARGED cycle before clearing its latch below
	// full-0.4 V; the next cycle returns to CHARGE.
	lastCharged := controller.Step(Input{ACOK: true, Battery: ina3221.Measurement{VoltageV: 12.1}})
	charging := controller.Step(Input{ACOK: true, Battery: ina3221.Measurement{VoltageV: 12.1}})
	if lastCharged.Snapshot.State != "CHARGED" || charging.Snapshot.State != "CHARGE" {
		t.Fatalf("charged latch transition = %s -> %s", lastCharged.Snapshot.State, charging.Snapshot.State)
	}
}

func TestACLossSwitchesRelayTwoBeforeRelayOne(t *testing.T) {
	decision := testController().Step(Input{
		ACOK:    false,
		Battery: ina3221.Measurement{VoltageV: 11.8, CurrentMA: -800},
	})
	if decision.Relay1High || !decision.Relay2High || !decision.Relay2First {
		t.Fatalf("unexpected discharge relay decision: %#v", decision)
	}
	if decision.SwitchDelay != 50*time.Millisecond || decision.Snapshot.State != "DISCHARGE" {
		t.Fatalf("unexpected switching delay/snapshot: %#v", decision)
	}
}

func TestCurrentPolarityNoiseAndBatteryBounds(t *testing.T) {
	controller := New(Config{
		BatteryEmptyVoltage: 9.3, BatteryFullVoltage: 12.6,
		CurrentNoiseFloorMA: 30, CurrentPolarity: -1, OutputCurrentPolarity: -1,
	})
	decision := controller.Step(Input{
		ACOK:    true,
		Battery: ina3221.Measurement{VoltageV: 15, CurrentMA: 20},
		Output:  ina3221.Measurement{VoltageV: 19, CurrentMA: 100},
	})
	if decision.Snapshot.CurrentMA != 0 || decision.Snapshot.OutputCurrentMA != -100 {
		t.Fatalf("current policy mismatch: %#v", decision.Snapshot)
	}
	if decision.Snapshot.Percent != 100 {
		t.Fatalf("battery percent = %d", decision.Snapshot.Percent)
	}
}

func TestLowVoltageShutdownIsRequestedOnce(t *testing.T) {
	controller := testController()
	input := Input{
		ACOK: false, Battery: ina3221.Measurement{VoltageV: 9.0}, Elapsed: 10 * time.Second,
	}
	if first := controller.Step(input); first.RequestShutdown {
		t.Fatal("shutdown requested too early")
	}
	if second := controller.Step(input); !second.RequestShutdown {
		t.Fatal("shutdown not requested at threshold")
	}
	if third := controller.Step(input); third.RequestShutdown {
		t.Fatal("shutdown requested more than once")
	}
}

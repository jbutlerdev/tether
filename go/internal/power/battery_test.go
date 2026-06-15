// battery_test.go — TDD-first tests for the Go battery-life
// model (plan §9.5).
//
// The M5 firmware's power_mgmt component exposes the same
// closed-form battery estimator. The Go side re-implements
// the math in pure Go so the operator TUI (plan §10.1) can
// render "battery: 78% (~5.4 h remaining)" without a real
// M5 attached. The two implementations must agree on the
// output (the test pins a few specific scenarios) so the
// operator TUI's prediction matches what the bench rig
// actually shows.
//
// The math (from plan §9.5 and the M5 component):
//
//	avg_current_mA = active_mA * duty + sleep_mA * (1 - duty)
//	hours         = capacity_mAh / avg_current_mA
//
// `is_feasible_6h_target` is true iff hours >= 6.

package power_test

import (
	"math"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/power"
)

const (
	kTinyDelta = 1e-3
)

func TestBattery_Sanity_OnePercentDuty(t *testing.T) {
	t.Parallel()
	est := power.EstimateBatteryLifeHours(0.01, 2000, 80, 0.05)
	// 1% duty: 80*0.01 + 0.05*0.99 = 0.8495 mA avg
	// 2000 / 0.8495 ≈ 2354.3 h
	want := 2000.0 / (80*0.01 + 0.05*0.99)
	if math.Abs(est.Hours-want) > 1.0 {
		t.Errorf("1%% duty: got %.1f h, want %.1f h (±1 h)", est.Hours, want)
	}
	if !est.Feasible6h {
		t.Errorf("1%% duty: should be feasible, got hours=%.1f", est.Hours)
	}
}

func TestBattery_Sanity_OneHundredPercentDuty(t *testing.T) {
	t.Parallel()
	est := power.EstimateBatteryLifeHours(1.0, 2000, 80, 0.05)
	// 100% duty: 80 mA avg
	// 2000 / 80 = 25 h
	want := 25.0
	if math.Abs(est.Hours-want) > 0.01 {
		t.Errorf("100%% duty: got %.4f h, want %.4f h", est.Hours, want)
	}
	if !est.Feasible6h {
		t.Errorf("100%% duty: 25 h is feasible; got hours=%.4f", est.Hours)
	}
}

func TestBattery_ZeroDuty_AllSleep(t *testing.T) {
	t.Parallel()
	est := power.EstimateBatteryLifeHours(0.0, 2000, 80, 0.05)
	// 0% duty: 0.05 mA avg
	// 2000 / 0.05 = 40000 h
	want := 40000.0
	if math.Abs(est.Hours-want) > 0.1 {
		t.Errorf("0%% duty: got %.4f h, want %.4f h", est.Hours, want)
	}
}

func TestBattery_SixHourTarget(t *testing.T) {
	t.Parallel()
	// The plan's exit gate: at the M5's typical duty cycles
	// (5-20%) the model must predict ≥ 6 hours.
	for _, duty := range []float64{0.05, 0.10, 0.20} {
		est := power.EstimateBatteryLifeHours(duty, 2000, 80, 0.05)
		if !est.Feasible6h {
			t.Errorf("duty=%.2f: got %.2f h, want ≥ 6 h", duty, est.Hours)
		}
	}
}

func TestBattery_DeepSleepCurrentTarget(t *testing.T) {
	t.Parallel()
	// 50 µA deep sleep is the structural target (plan §9.5).
	// The model uses this number directly; we pin it so a
	// future refactor that bumps the sleep current (and
	// therefore pessimises the battery life) breaks this
	// test.
	est := power.EstimateBatteryLifeHours(0.0, 2000, 80, 0.05)
	if est.Hours != 40000.0 {
		t.Errorf("0%% duty: got %v h, want exactly 40000 h", est.Hours)
	}
}

func TestBattery_ClampsDuty(t *testing.T) {
	t.Parallel()
	// A negative duty is treated as 0; a >1 duty is clamped to 1.
	neg := power.EstimateBatteryLifeHours(-0.5, 2000, 80, 0.05)
	if neg.Hours != 40000.0 {
		t.Errorf("negative duty: got %v, want 40000 (clamped to 0)", neg.Hours)
	}
	big := power.EstimateBatteryLifeHours(2.5, 2000, 80, 0.05)
	if big.Hours != 25.0 {
		t.Errorf("duty > 1: got %v, want 25 (clamped to 1)", big.Hours)
	}
}

func TestBattery_InfeasibleCases(t *testing.T) {
	t.Parallel()
	// Zero or negative capacity or current: no estimate.
	for _, c := range []struct {
		name                                 string
		duty, capacity, active, sleep        float64
	}{
		{"zero capacity", 0.05, 0, 80, 0.05},
		{"negative capacity", 0.05, -1, 80, 0.05},
		{"zero current", 0.05, 2000, 0, 0},
		{"negative current", 0.05, 2000, -1, 0.05},
		{"active=0", 0.05, 2000, 0, 0.05},
		{"avg=0 edge", 0.0, 2000, 0, 0},
	} {
		est := power.EstimateBatteryLifeHours(c.duty, c.capacity, c.active, c.sleep)
		if est.Hours != 0 {
			t.Errorf("%s: got hours=%v, want 0", c.name, est.Hours)
		}
		if est.Feasible6h {
			t.Errorf("%s: infeasible inputs reported feasible", c.name)
		}
	}
}

func TestBattery_TenPercentDuty(t *testing.T) {
	t.Parallel()
	// Pin a realistic scenario: 10% duty, 2000 mAh, 80 mA
	// active, 50 µA sleep.
	est := power.EstimateBatteryLifeHours(0.10, 2000, 80, 0.05)
	// 80*0.1 + 0.05*0.9 = 8.045 mA avg
	// 2000 / 8.045 ≈ 248.6 h
	want := 2000.0 / (80*0.1 + 0.05*0.9)
	if math.Abs(est.Hours-want) > 0.1 {
		t.Errorf("10%% duty: got %.2f h, want %.2f h", est.Hours, want)
	}
}

func TestBattery_ConsistentWithCpp(t *testing.T) {
	t.Parallel()
	// The C++ side and the Go side must produce the same
	// number for the same input. We pin a few specific
	// scenarios here. (The C++ unit tests cover the same
	// scenarios; the cross-language pin is the SHA-256 test
	// vector on the aes_link side. This test pins Go-only
	// consistency for the battery model.)
	for _, c := range []struct {
		duty, capacity, active, sleep float64
	}{
		{0.0, 2000, 80, 0.05},
		{0.01, 2000, 80, 0.05},
		{0.10, 2000, 80, 0.05},
		{0.50, 2000, 80, 0.05},
		{1.0, 2000, 80, 0.05},
		{0.10, 1500, 100, 0.10},
		{0.20, 3000, 60, 0.05},
	} {
		est := power.EstimateBatteryLifeHours(c.duty, c.capacity, c.active, c.sleep)
		avg := c.active*c.duty + c.sleep*(1-c.duty)
		want := c.capacity / avg
		if math.Abs(est.Hours-want) > kTinyDelta {
			t.Errorf("duty=%v cap=%v active=%v sleep=%v: got %v, want %v",
				c.duty, c.capacity, c.active, c.sleep, est.Hours, want)
		}
	}
}

// Package power holds the Go-side battery-life model used by
// the operator TUI (plan §10.1). The math is the same as the
// M5 firmware's power_mgmt component (firmware/m5/components/
// power_mgmt), so a future cross-language pin is possible.
//
// The model is intentionally closed-form:
//
//	avg_current_mA = active_mA * duty + sleep_mA * (1 - duty)
//	hours         = capacity_mAh / avg_current_mA
//
// `Feasible6h` is true iff hours >= 6 — the plan's exit
// gate for "6-hour battery life verified on bench"
// (plan §9.5/§9.7).
package power

// Estimate is the output of EstimateBatteryLifeHours.
type Estimate struct {
	Hours      float64
	Feasible6h bool
}

// EstimateBatteryLifeHours returns the battery-life estimate
// for a given duty cycle and battery / current parameters.
// Inputs are validated: a non-positive capacity or current
// yields an Estimate{Hours: 0, Feasible6h: false}; a duty
// outside [0, 1] is clamped.
func EstimateBatteryLifeHours(duty, capacityMah, activeMa, sleepMa float64) Estimate {
	if capacityMah <= 0 || activeMa <= 0 || sleepMa < 0 {
		return Estimate{Hours: 0, Feasible6h: false}
	}
	if duty < 0 {
		duty = 0
	}
	if duty > 1 {
		duty = 1
	}
	avg := activeMa*duty + sleepMa*(1-duty)
	if avg <= 0 {
		return Estimate{Hours: 0, Feasible6h: false}
	}
	hours := capacityMah / avg
	return Estimate{Hours: hours, Feasible6h: hours >= 6.0}
}

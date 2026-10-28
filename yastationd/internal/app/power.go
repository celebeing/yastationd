package app

import (
	"strconv"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Power self-diagnosis: the speakers want a USB-C PD contract (12 V). A plain 5 V supply or a
// cable without the CC line still boots them, but the amp loses headroom and loud peaks can
// brown them out — and nothing tells the user. So: publish the contract to HA and, once per
// boot, say the stock «блок питания не подходит» line when there's no healthy contract.

const (
	powerFirstCheck = 5 * time.Second
	powerEvery      = 5 * time.Minute // each read makes the PD driver re-query (and log)
	// Survives daemon respawns, not reboots: the line is said once per boot.
	powerAlertedMarker = "/tmp/yastationd.power_alerted"
)

func (a *App) powerLoop(stop <-chan struct{}) {
	if a.pdDir == "" {
		return
	}
	wait := powerFirstCheck
	for {
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
		wait = powerEvery
		pd, err := hw.ReadPD(a.pdDir)
		if err != nil {
			a.log.Debug("power: read", "err", err)
			continue
		}
		bad := powerProblem(pd, a.prof.Power.MinMilliVolts)
		a.publish(a.topics.State("supply_voltage"), strconv.FormatFloat(float64(pd.MilliVolts)/1000, 'f', 1, 64))
		a.publish(a.topics.State("power_problem"), onOff(bad))
		if bad {
			a.powerAlert(pd)
		}
	}
}

// powerProblem: no healthy PD contract at or above minMV.
func powerProblem(pd hw.PD, minMV int) bool {
	return pd.Health != "Good" || pd.MilliVolts < minMV
}

func (a *App) powerAlert(pd hw.PD) {
	if !a.firstThisBoot(powerAlertedMarker) {
		return
	}
	a.log.Warn("power: no healthy USB-C PD contract — use a PD charger (12 V) and a C-to-C cable",
		"mv", pd.MilliVolts, "ma", pd.MilliAmps, "health", pd.Health)
	a.hook("power_problem")
}

package git

import (
	"fmt"
	"log"
)

type DiscardPolicy struct {
	WarnLimit    int
	FailOnExcess bool
}

type discardTracker struct {
	name         string
	warnLimit    int
	failOnExcess bool
	discarded    int
}

func newDiscardTracker(name string, policy DiscardPolicy) *discardTracker {
	return &discardTracker{
		name:         name,
		warnLimit:    policy.WarnLimit,
		failOnExcess: policy.FailOnExcess,
	}
}

func (d *discardTracker) record(reason string) error {
	d.discarded++
	if d.warnLimit < 0 || d.discarded <= d.warnLimit {
		log.Printf("warning: %s ignored entry %d: %s", d.name, d.discarded, reason)
	} else if d.discarded == d.warnLimit+1 {
		log.Printf("warning: %s discards exceeded warn limit (%d); suppressing further logs", d.name, d.warnLimit)
	}

	if d.failOnExcess && d.warnLimit >= 0 && d.discarded > d.warnLimit {
		return fmt.Errorf("%s discarded %d entries exceeding limit %d", d.name, d.discarded, d.warnLimit)
	}

	return nil
}

func (d *discardTracker) finalize() {
	if d.warnLimit >= 0 && d.discarded > d.warnLimit {
		log.Printf("warning: %s discarded %d entries (limit %d)", d.name, d.discarded, d.warnLimit)
	}
}

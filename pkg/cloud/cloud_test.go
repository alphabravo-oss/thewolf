package cloud

import "testing"

func TestNopMeterRecordsNothing(t *testing.T) {
	NopMeter{}.Record("scan.completed", 1)
}

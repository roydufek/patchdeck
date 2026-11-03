package scheduler

import (
	"testing"

	"patchdeck/api/internal/models"
)

// Apply jobs patch the Patchdeck host last (its own network/docker restarts would cut the
// others' applies), keeping everyone else in their original order.
func TestSelfLast(t *testing.T) {
	in := []models.Host{{Name: "arc"}, {Name: "janus"}, {Name: "cosmos"}, {Name: "nebula"}}
	out := selfLast(in, func(h models.Host) bool { return h.Name == "janus" })
	got := ""
	for _, h := range out {
		got += h.Name + " "
	}
	if got != "arc cosmos nebula janus " {
		t.Fatalf("order: %s", got)
	}
	if len(selfLast(in, func(models.Host) bool { return false })) != 4 {
		t.Fatal("no self host: unchanged")
	}
}

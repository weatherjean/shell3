package scaffold

import (
	"strings"
	"testing"
)

func TestKitPinsFreshInboxAndScheduleInventoryChecks(t *testing.T) {
	for _, contract := range []string{
		"perform a fresh inbox query; never reuse an earlier count",
		"use shell3 schedule list and inspect the declared wrkfile",
		"Run directories are execution history, not an inventory of definitions",
	} {
		if !strings.Contains(Kit, contract) {
			t.Fatalf("starter kit missing operational contract %q", contract)
		}
	}
}

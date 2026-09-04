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

func TestKitUsesLazySDFileEditingGuidance(t *testing.T) {
	for _, contract := range []string{
		"exactly two core tools: bash and bash_bg",
		"Read sd-file-editing before editing whenever",
		"(skill sd-file-editing",
		"sd exits successfully when nothing matched",
		"Do not use sed -i",
	} {
		if !strings.Contains(Kit, contract) {
			t.Fatalf("starter kit missing editing contract %q", contract)
		}
	}
}

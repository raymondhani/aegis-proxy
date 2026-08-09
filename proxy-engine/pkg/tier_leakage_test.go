// Package pkg holds Clean Architecture layering regression tests
// (Constitution Principle I): the OSS engine must contain zero Tier 2/3
// vocabulary, even in compiled artifacts.
package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deniedIdentifiers are proprietary Tier 2/3 symbols that must never appear
// in OSS .go source. See research.md R4: the scan targets specific policy-sync
// and adaptive-throttling identifiers, not generic substrings like "grpc",
// which OpenTelemetry's OTLP exporters legitimately use.
var deniedIdentifiers = []string{
	"MLAdaptiveThrottling",
	"ml_adaptive_throttling",
	"AdaptiveRateLimiter",
	"SessionBaseline",
	"PlanTier",
	"kernel_bypass",
	"KernelBypass",
	"policies.Manager",
	"DEBUG_BYPASS_STRIPE",
}

// allowlistedFiles are known-legitimate uses that would otherwise false-positive
// (research.md R4): OpenTelemetry's OTLP gRPC exporters are not a policy-sync leak.
var allowlistedFiles = map[string]bool{
	filepath.Join("infrastructure", "observability", "otel.go"): true,
}

func TestNoTierLeakageInOSSSource(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve pkg root: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		if allowlistedFiles[rel] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for _, denied := range deniedIdentifiers {
			if strings.Contains(string(content), denied) {
				t.Errorf("%s: contains denied Tier 2/3 identifier %q (Principle I: OSS must carry zero Tier 2/3 vocabulary)", rel, denied)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg: %v", err)
	}
}

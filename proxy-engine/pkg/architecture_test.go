// Package pkg holds Clean Architecture layering regression tests
// (Constitution Principle III): domain -> usecase -> infrastructure,
// dependencies point strictly inward.
package pkg

import (
	"go/build"
	"strings"
	"testing"
)

const modulePath = "github.com/raymondhani/aegis-proxy/proxy-engine"

func importsOf(t *testing.T, dir string) []string {
	t.Helper()
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		if _, ok := err.(*build.NoGoError); ok {
			return nil
		}
		t.Fatalf("importing %s: %v", dir, err)
	}
	var all []string
	all = append(all, pkg.Imports...)
	all = append(all, pkg.TestImports...)
	all = append(all, pkg.XTestImports...)
	return all
}

// TestDomainImportsNothingInternal asserts pkg/domain declares only types and
// interfaces, with no dependency on usecase or infrastructure.
func TestDomainImportsNothingInternal(t *testing.T) {
	for _, imp := range importsOf(t, "domain") {
		if strings.HasPrefix(imp, modulePath) {
			t.Errorf("pkg/domain must not import internal packages, found: %s", imp)
		}
	}
}

// TestUsecaseNeverImportsInfrastructure asserts pkg/usecase depends only on
// domain, never on infrastructure (which would invert the dependency arrow).
func TestUsecaseNeverImportsInfrastructure(t *testing.T) {
	for _, imp := range importsOf(t, "usecase") {
		if strings.HasPrefix(imp, modulePath+"/pkg/infrastructure") {
			t.Errorf("pkg/usecase must not import pkg/infrastructure, found: %s", imp)
		}
	}
}

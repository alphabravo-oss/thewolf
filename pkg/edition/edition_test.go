package edition

import (
	"net/http"
	"testing"
)

func TestContractVersion(t *testing.T) {
	if ContractVersion == "" {
		t.Fatal("contract version")
	}
	if Product(Community) != "Wolf Community" || Product(Enterprise) != "Wolf Enterprise" {
		t.Fatal("product names")
	}
}

func TestCommunityRegistryRejectsNilAndRecordsModule(t *testing.T) {
	r := New(Community)
	if err := r.Add(nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(CommunityModule{}); err != nil {
		t.Fatal(err)
	}
	mods := r.Modules()
	if len(mods) != 1 || mods[0] != Community {
		t.Fatalf("modules = %#v", mods)
	}
}

type proof struct{ Nop }

func (proof) Name() string { return "proof" }
func (p proof) RegisterScannerCatalogs(c CatalogRegistry) error {
	c.RegisterCatalog("enterprise.proof-catalog", "stub")
	return nil
}
func (p proof) RegisterJobs(j JobRegistry) error {
	j.RegisterJob("enterprise.proof-job", func() {})
	return nil
}
func (p proof) RegisterPolicies(pReg PolicyRegistry) error {
	pReg.RegisterPolicy("enterprise.proof-policy", "stub")
	return nil
}
func (p proof) RegisterReports(rep ReportRegistry) error {
	rep.RegisterReport("enterprise.proof-report", "stub")
	return nil
}
func (proof) RegisterRoutes(r RouteRegistry) error {
	r.Handle(http.MethodGet, "/enterprise/proof", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	return nil
}
func (proof) UIManifest() UIManifest {
	return UIManifest{Routes: []UIRoute{{Path: "/enterprise/proof", Title: "Proof"}}}
}

func TestOverlayProofRegistersWithoutImportingInternal(t *testing.T) {
	r := New(Enterprise)
	if err := r.Add(CommunityModule{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(proof{}); err != nil {
		t.Fatal(err)
	}
	if r.Name() != Enterprise {
		t.Fatalf("name = %q", r.Name())
	}
	if len(r.Catalogs()) != 1 {
		t.Fatalf("catalogs = %#v", r.Catalogs())
	}
	if len(r.Jobs()) == 0 || len(r.Policies()) == 0 || len(r.Reports()) == 0 {
		t.Fatalf("jobs/policies/reports missing: %#v %#v %#v", r.Jobs(), r.Policies(), r.Reports())
	}
	if len(r.UI().Routes) != 1 || r.UI().Routes[0].Path != "/enterprise/proof" {
		t.Fatalf("ui = %#v", r.UI())
	}
	rts := r.Routes()
	if len(rts) != 1 || rts[0].Pattern != "/enterprise/proof" {
		t.Fatalf("routes = %#v", rts)
	}
}

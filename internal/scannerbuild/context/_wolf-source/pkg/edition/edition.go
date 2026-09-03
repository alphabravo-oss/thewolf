// Package edition is the public composition root for Community, Enterprise, and Cloud.
//
// Business logic stays in internal/. This package is interfaces, DTOs, and a
// process-wide registry the Community binary and the private overlay both use.
package edition

import (
	"net/http"
	"sync"
)

const (
	Community       = "community"
	Enterprise      = "enterprise"
	Cloud           = "cloud"
	ContractVersion = "1.0.0"
)

// Product is the human edition name. UI and /version must use this, not a
// hardcoded "Wolf Community" string.
func Product(name string) string {
	switch name {
	case Enterprise:
		return "Wolf Enterprise"
	case Cloud:
		return "Wolf Cloud"
	default:
		return "Wolf Community"
	}
}

// Module is an edition module. Community registers only Community modules.
// Enterprise registers Community modules plus private ones.
type Module interface {
	Name() string
	RegisterServices(ServiceRegistry) error
	RegisterRoutes(RouteRegistry) error
	RegisterJobs(JobRegistry) error
	RegisterPolicies(PolicyRegistry) error
	RegisterReports(ReportRegistry) error
	RegisterScannerCatalogs(CatalogRegistry) error
	UIManifest() UIManifest
}

type ServiceRegistry interface {
	RegisterService(name string, svc any)
}

type RouteRegistry interface {
	Handle(method, pattern string, h http.Handler)
}

type JobRegistry interface {
	RegisterJob(name string, fn func())
}

type PolicyRegistry interface {
	RegisterPolicy(name string, spec any)
}

type ReportRegistry interface {
	RegisterReport(name string, spec any)
}

type CatalogRegistry interface {
	RegisterCatalog(name string, spec any)
}

// UIManifest lists UI routes composed at build time. Public UI must not hide
// complete Enterprise pages behind a flag; Enterprise adds routes privately.
type UIManifest struct {
	Routes []UIRoute `json:"routes,omitempty"`
}

type UIRoute struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// Nop is a Module with empty registrations.
type Nop struct{}

func (Nop) Name() string                                  { return "nop" }
func (Nop) RegisterServices(ServiceRegistry) error        { return nil }
func (Nop) RegisterRoutes(RouteRegistry) error            { return nil }
func (Nop) RegisterJobs(JobRegistry) error                { return nil }
func (Nop) RegisterPolicies(PolicyRegistry) error         { return nil }
func (Nop) RegisterReports(ReportRegistry) error          { return nil }
func (Nop) RegisterScannerCatalogs(CatalogRegistry) error { return nil }
func (Nop) UIManifest() UIManifest                        { return UIManifest{} }

// Registry is the process-wide edition host.
type Registry struct {
	mu       sync.Mutex
	name     string
	modules  []Module
	services map[string]any
	routes   []mountedRoute
	jobs     map[string]func()
	policies map[string]any
	reports  map[string]any
	catalogs map[string]any
	ui       UIManifest
}

type mountedRoute struct {
	Method, Pattern string
	Handler         http.Handler
}

// Default is the Community registry. The Enterprise binary replaces Name.
var Default = New(Community)

func New(name string) *Registry {
	return &Registry{
		name:     name,
		services: map[string]any{},
		jobs:     map[string]func(){},
		policies: map[string]any{},
		reports:  map[string]any{},
		catalogs: map[string]any{},
	}
}

func (r *Registry) Name() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.name
}

func (r *Registry) SetName(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.name = name
}

func (r *Registry) Add(m Module) error {
	if m == nil {
		return nil
	}
	if err := m.RegisterServices(r); err != nil {
		return err
	}
	if err := m.RegisterRoutes(r); err != nil {
		return err
	}
	if err := m.RegisterJobs(r); err != nil {
		return err
	}
	if err := m.RegisterPolicies(r); err != nil {
		return err
	}
	if err := m.RegisterReports(r); err != nil {
		return err
	}
	if err := m.RegisterScannerCatalogs(r); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules = append(r.modules, m)
	man := m.UIManifest()
	r.ui.Routes = append(r.ui.Routes, man.Routes...)
	return nil
}

func (r *Registry) Modules() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.modules))
	for _, m := range r.modules {
		out = append(out, m.Name())
	}
	return out
}

func (r *Registry) Catalogs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.catalogs))
	for name := range r.catalogs {
		out = append(out, name)
	}
	return out
}

func (r *Registry) Jobs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.jobs))
	for name := range r.jobs {
		out = append(out, name)
	}
	return out
}

func (r *Registry) Policies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.policies))
	for name := range r.policies {
		out = append(out, name)
	}
	return out
}

func (r *Registry) Reports() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.reports))
	for name := range r.reports {
		out = append(out, name)
	}
	return out
}

func (r *Registry) UI() UIManifest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ui
}

func (r *Registry) RegisterService(name string, svc any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[name] = svc
}

func (r *Registry) Handle(method, pattern string, h http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, mountedRoute{Method: method, Pattern: pattern, Handler: h})
}

// HTTPMux is the subset of chi used to mount overlay routes.
type HTTPMux interface {
	Method(method, pattern string, h http.Handler)
}

func (r *Registry) Mount(mux HTTPMux) {
	if mux == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.routes {
		if rt.Handler == nil {
			continue
		}
		mux.Method(rt.Method, rt.Pattern, rt.Handler)
	}
}

func (r *Registry) Service(name string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.services[name]
	return v, ok
}

func (r *Registry) Routes() []struct{ Method, Pattern string } {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]struct{ Method, Pattern string }, len(r.routes))
	for i, rt := range r.routes {
		out[i] = struct{ Method, Pattern string }{rt.Method, rt.Pattern}
	}
	return out
}

func (r *Registry) RegisterJob(name string, fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[name] = fn
}

func (r *Registry) RegisterPolicy(name string, spec any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[name] = spec
}

func (r *Registry) RegisterReport(name string, spec any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports[name] = spec
}

func (r *Registry) RegisterCatalog(name string, spec any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.catalogs[name] = spec
}

// CommunityModule is the built-in Community edition module.
type CommunityModule struct{ Nop }

func (CommunityModule) Name() string { return Community }

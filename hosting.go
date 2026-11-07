package main

import (
	"errors"
	"net/http"

	"panel/internal/apps"
	"panel/internal/jobrunner"
	"panel/internal/management"
	"panel/internal/sites"
)

type hostingServices struct {
	authority *management.Authority
	jobs      *jobrunner.Runner
	apps      *apps.Service
	sites     *sites.Service
}

func newHostingServices(dataDir, panelHost string, panelPort int, audit func(string, string)) (*hostingServices, error) {
	authority := management.New()
	return openHostingServices(dataDir, panelHost, panelPort, audit, authority, authority.Check)
}

// The permission parameter is internal for isolated integration tests. Production
// always uses the fixed-path, actual-identity Authority; it has no HTTP/flag bypass.
func openHostingServices(dataDir, panelHost string, panelPort int, audit func(string, string), authority *management.Authority, permission func() error) (*hostingServices, error) {
	jobs, err := jobrunner.New(dataDir)
	if err != nil {
		return nil, err
	}
	appService, err := apps.New(apps.Options{Jobs: jobs, Permission: permission, Audit: audit})
	if err != nil {
		_ = jobs.Close()
		return nil, err
	}
	siteService, err := sites.New(sites.Options{DataDir: dataDir, Jobs: jobs, Permission: permission, Runtime: appService.Capabilities, Audit: audit, PanelHost: panelHost, PanelPort: panelPort})
	if err != nil {
		_ = appService.Close()
		_ = jobs.Close()
		return nil, err
	}
	return &hostingServices{authority: authority, jobs: jobs, apps: appService, sites: siteService}, nil
}

func (s *hostingServices) Register(mux *http.ServeMux) {
	s.authority.Register(mux)
	s.jobs.Register(mux)
	s.apps.Register(mux)
	s.sites.Register(mux)
}

func (s *hostingServices) Close() error {
	// Let an in-flight site transaction finish its bounded rollback before the
	// shared task runner closes its persistent state and cancels queued work.
	siteErr := s.sites.Close()
	appErr := s.apps.Close()
	jobErr := s.jobs.Close()
	return errors.Join(jobErr, appErr, siteErr)
}

package api

import (
	"context"
	"net/http"

	"github.com/rhw/m365backup/internal/db"
)

type jobOverviewRow struct {
	db.Job
	TenantName string
}

func (s *Server) tenantNames(ctx context.Context) (map[string]string, error) {
	list, err := s.DB.ListTenants(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for i := range list {
		out[list[i].ID] = list[i].Name
	}
	return out, nil
}

func attachTenantNames(jobs []db.Job, names map[string]string) []jobOverviewRow {
	out := make([]jobOverviewRow, 0, len(jobs))
	for i := range jobs {
		name := names[jobs[i].TenantID]
		if name == "" {
			name = jobs[i].TenantID
		}
		out = append(out, jobOverviewRow{Job: jobs[i], TenantName: name})
	}
	return out
}

func (s *Server) jobOverviewData(ctx context.Context) (map[string]any, error) {
	names, err := s.tenantNames(ctx)
	if err != nil {
		return nil, err
	}
	active, err := s.DB.ListActiveJobs(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.DB.ListRecentJobs(ctx, 20)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ActiveJobs": attachTenantNames(active, names),
		"RecentJobs": attachTenantNames(recent, names),
		"JobCounts":  countJobs(active),
	}, nil
}

func (s *Server) handleJobsOverview(w http.ResponseWriter, r *http.Request) {
	data, err := s.jobOverviewData(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "jobs.html", data)
}

func (s *Server) handleJobsOverviewPartial(w http.ResponseWriter, r *http.Request) {
	data, err := s.jobOverviewData(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "jobs_overview_partial.html", data)
}

func (s *Server) apiListActiveJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.DB.ListActiveJobs(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, jobs)
}

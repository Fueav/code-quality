package main

import "os"

type Resource struct {
	ID       string
	TenantID string
	Value    string
}

type Repository struct{ rows []Resource }

func (repository *Repository) Update(id, tenantID, value string) bool {
	for index := range repository.rows {
		row := &repository.rows[index]
		if row.ID == id && row.TenantID == tenantID {
			row.Value = value
			return true
		}
	}
	return false
}

func HandlePublicUpdate(repository *Repository, authenticatedTenant, requestedID, value string) bool {
	return repository.Update(requestedID, authenticatedTenant, value)
}

func main() {
	repository := &Repository{rows: []Resource{
		{ID: "shared-id", TenantID: "victim", Value: "victim-data"},
		{ID: "shared-id", TenantID: "attacker", Value: "attacker-data"},
	}}
	HandlePublicUpdate(repository, "attacker", "shared-id", "changed")
	if repository.rows[0].Value != "victim-data" {
		os.Exit(2)
	}
}

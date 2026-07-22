package main

import "os"

type Key struct {
	TenantID   string
	ResourceID string
}

type Repository struct {
	values map[Key]string
	order  []Key
}

func (repository *Repository) Update(tenantID, resourceID, value string) {
	repository.values[Key{TenantID: tenantID, ResourceID: resourceID}] = value
}

func HandleUpdate(repository *Repository, authenticatedTenant, requestedID, value string) {
	repository.Update(authenticatedTenant, requestedID, value)
}

func main() {
	repository := &Repository{values: map[Key]string{
		{TenantID: "tenant-a", ResourceID: "shared-id"}: "a",
		{TenantID: "tenant-b", ResourceID: "shared-id"}: "b",
	}, order: []Key{
		{TenantID: "tenant-b", ResourceID: "shared-id"},
		{TenantID: "tenant-a", ResourceID: "shared-id"},
	}}
	HandleUpdate(repository, "tenant-a", "shared-id", "updated-a")
	if repository.values[Key{TenantID: "tenant-b", ResourceID: "shared-id"}] != "b" {
		os.Exit(2)
	}
}

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

func (repository *Repository) Update(resourceID, value string) {
	for _, key := range repository.order {
		if key.ResourceID == resourceID {
			repository.values[key] = value
			return
		}
	}
}

func HandleUpdate(repository *Repository, _ string, requestedID, value string) {
	repository.Update(requestedID, value)
}

func main() {
	repository := &Repository{values: map[Key]string{
		{TenantID: "tenant-b", ResourceID: "shared-id"}: "b",
		{TenantID: "tenant-a", ResourceID: "shared-id"}: "a",
	}, order: []Key{
		{TenantID: "tenant-b", ResourceID: "shared-id"},
		{TenantID: "tenant-a", ResourceID: "shared-id"},
	}}
	HandleUpdate(repository, "tenant-a", "shared-id", "updated-a")
	if repository.values[Key{TenantID: "tenant-b", ResourceID: "shared-id"}] != "b" {
		os.Exit(2)
	}
}

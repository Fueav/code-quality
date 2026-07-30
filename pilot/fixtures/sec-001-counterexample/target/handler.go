package access

type GatewayIdentity struct {
	TenantID string
	Role     string
}

type Gateway struct{}

func (Gateway) Verify(signedHeader string) (GatewayIdentity, bool) {
	if signedHeader != "gateway-signature:tenant-a:editor" {
		return GatewayIdentity{}, false
	}
	return GatewayIdentity{TenantID: "tenant-a", Role: "editor"}, true
}

type Repository interface {
	LookupForTenant(tenantID, resourceID string) bool
}

func Handle(gateway Gateway, repository Repository, signedHeader, resourceID string) bool {
	identity, valid := gateway.Verify(signedHeader)
	if !valid || identity.Role != "editor" {
		return false
	}
	return repository.LookupForTenant(identity.TenantID, resourceID)
}

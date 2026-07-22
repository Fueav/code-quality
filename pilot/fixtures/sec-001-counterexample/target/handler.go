package access

type GatewayIdentity struct{ TenantID string }

type Gateway struct{}

func (Gateway) Verify(signedHeader string) (GatewayIdentity, bool) {
	if signedHeader != "gateway-signature:tenant-a" {
		return GatewayIdentity{}, false
	}
	return GatewayIdentity{TenantID: "tenant-a"}, true
}

type Repository interface {
	LookupForTenant(tenantID, resourceID string) bool
}

func Handle(gateway Gateway, repository Repository, signedHeader, resourceID string) bool {
	identity, valid := gateway.Verify(signedHeader)
	if !valid {
		return false
	}
	return repository.LookupForTenant(identity.TenantID, resourceID)
}

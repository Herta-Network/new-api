package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
)

type permissionCatalog struct {
	Resources []authz.ResourceDefinition `json:"resources"`
	Roles     []authz.RoleDescriptor     `json:"roles"`
}

// GetPermissionCatalog returns the permission schema used by the client to
// render the permission editor: the registry of resources with their actions
// and display label keys, plus the roles with their baseline grant matrices.
// Defining it in the authz package keeps the schema in a single place.
//
//	@Summary	获取权限目录
//	@Tags		权限
//	@Success	200	{object}	common.ApiResponse{data=permissionCatalog}
//	@Security	AdminAuth
//	@Router		/api/authz/catalog [get]
func GetPermissionCatalog(c *gin.Context) {
	common.ApiSuccess(c, permissionCatalog{
		Resources: authz.Catalog(),
		Roles:     authz.Roles(),
	})
}

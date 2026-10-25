package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetAllVendors uses the same paged filters and counts as the search endpoint.
//
//	@Summary	列出供应商
//	@Tags		供应商
//	@Param		keyword		query		string	false	"在名称和描述中搜索"
//	@Param		association	query		string	false	"linked 只看有模型的供应商，unlinked 只看没有模型的"
//	@Param		p			query		int		false	"页码，从 1 开始"
//	@Param		page_size	query		int		false	"每页条数，最多 100"
//	@Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Vendor}}
//	@Failure	400			{object}	common.ApiResponse	"请求无效或操作失败"
//	@Security	AdminAuth
//	@Router		/api/vendors/ [get]
func GetAllVendors(c *gin.Context) { SearchVendors(c) }

// @Summary	搜索供应商
// @Tags		供应商
// @Param		keyword		query		string	false	"在名称和描述中搜索"
// @Param		association	query		string	false	"linked 只看有模型的供应商，unlinked 只看没有模型的"
// @Param		p			query		int		false	"页码，从 1 开始"
// @Param		page_size	query		int		false	"每页条数，最多 100"
// @Success	200			{object}	common.ApiResponse{data=common.PageInfo{items=[]model.Vendor}}
// @Failure	400			{object}	common.ApiResponse	"请求无效或操作失败"
// @Security	AdminAuth
// @Router		/api/vendors/search [get]
func SearchVendors(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	vendors, total, err := model.SearchVendors(c.Query("keyword"), pageInfo.GetStartIdx(), pageInfo.GetPageSize(), c.Query("association"))
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(vendors)
	common.ApiSuccess(c, pageInfo)
}

// GetVendorMeta 根据 ID 获取供应商
//
//	@Summary	获取供应商
//	@Tags		供应商
//	@Param		id	path		int	true	"供应商 ID"
//	@Success	200	{object}	common.ApiResponse{data=model.Vendor}
//	@Failure	400	{object}	common.ApiResponse	"请求无效或操作失败"
//	@Security	AdminAuth
//	@Router		/api/vendors/{id} [get]
func GetVendorMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	v, err := model.GetVendorByID(id)
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	common.ApiSuccess(c, v)
}

// CreateVendorMeta 新建供应商
//
//	@Summary	创建供应商
//	@Tags		供应商
//	@Param		body	body		model.Vendor	true	"供应商；用到 name、description、icon"
//	@Success	200		{object}	common.ApiResponse{data=model.Vendor}
//	@Failure	400		{object}	common.ApiResponse	"请求无效或操作失败"
//	@Security	AdminAuth
//	@Router		/api/vendors/ [post]
func CreateVendorMeta(c *gin.Context) {
	var v model.Vendor
	if err := c.ShouldBindJSON(&v); err != nil {
		vendorAPIError(c, err)
		return
	}
	if err := v.Insert(); err != nil {
		vendorAPIError(c, err)
		return
	}
	recordManageAudit(c, "vendor.metadata.save", map[string]any{"vendor_id": v.Id, "name": v.Name})
	common.ApiSuccess(c, &v)
}

// UpdateVendorMeta 更新供应商
//
//	@Summary	更新供应商
//	@Tags		供应商
//	@Param		body	body		model.Vendor	true	"供应商；按 id 更新 name、description、icon，带 version 时先校验记录未变化"
//	@Success	200		{object}	common.ApiResponse{data=model.Vendor}
//	@Failure	400		{object}	common.ApiResponse	"请求无效或操作失败"
//	@Failure	409		{object}	common.ApiResponse	"记录已变化，code 为 VENDOR_CONFLICT"
//	@Security	AdminAuth
//	@Router		/api/vendors/ [put]
func UpdateVendorMeta(c *gin.Context) {
	var v model.Vendor
	if err := c.ShouldBindJSON(&v); err != nil {
		vendorAPIError(c, err)
		return
	}
	if v.Id == 0 {
		common.ApiErrorT(c, "Missing vendor ID")
		return
	}
	if err := v.Update(); err != nil {
		vendorAPIError(c, err)
		return
	}
	recordManageAudit(c, "vendor.metadata.save", map[string]any{"vendor_id": v.Id, "name": v.Name})
	common.ApiSuccess(c, &v)
}

// DeleteVendorMeta 删除供应商
//
//	@Summary	删除供应商
//	@Tags		供应商
//	@Param		id	path		int	true	"供应商 ID"
//	@Success	200	{object}	common.ApiResponse
//	@Failure	400	{object}	common.ApiResponse	"请求无效或操作失败"
//	@Failure	409	{object}	common.ApiResponse	"记录已变化或不存在时 code 为 VENDOR_CONFLICT；供应商仍被模型引用时 code 为 VENDOR_REFERENCED，reference_counts 为各供应商被引用的模型数"
//	@Security	AdminAuth
//	@Router		/api/vendors/{id} [delete]
func DeleteVendorMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	if err := model.DeleteVendors([]int{id}); err != nil {
		vendorAPIError(c, err)
		return
	}
	recordManageAudit(c, "vendor.metadata.delete", map[string]any{"vendor_id": id})
	common.ApiSuccess(c, nil)
}

func vendorAPIError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	payload := gin.H{"success": false, "message": err.Error()}
	var references *model.VendorReferenceError
	if errors.Is(err, model.ErrVendorConflict) {
		status = http.StatusConflict
		payload["code"] = "VENDOR_CONFLICT"
	}
	if errors.As(err, &references) {
		status = http.StatusConflict
		payload["code"] = "VENDOR_REFERENCED"
		payload["reference_counts"] = references.Counts
	}
	c.JSON(status, payload)
}

// @Summary		预览供应商批量操作
// @Description	不做修改；返回的 version 用作应用时的 expected_version。
// @Tags			供应商
// @Param			body	body		model.VendorOperation	true	"action 为 assign（把 model_ids 指派给 target_vendor_id，0 为清除）、merge（把 vendor_ids 合并到 target_vendor_id）或 delete（删除 vendor_ids）"
// @Success		200		{object}	common.ApiResponse{data=model.VendorOperationPreview}
// @Failure		400		{object}	common.ApiResponse	"请求无效或操作失败"
// @Failure		409		{object}	common.ApiResponse	"记录已变化或不存在时 code 为 VENDOR_CONFLICT；供应商仍被模型引用时 code 为 VENDOR_REFERENCED，reference_counts 为各供应商被引用的模型数"
// @Security		AdminAuth
// @Router			/api/vendors/operations/preview [post]
func PreviewVendorOperation(c *gin.Context) {
	var request model.VendorOperation
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		vendorAPIError(c, err)
		return
	}
	preview, err := model.PreviewVendorOperation(request)
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

// @Summary	应用供应商批量操作
// @Tags		供应商
// @Param		body	body		model.VendorOperation	true	"与预览相同的操作；expected_version 必填，为预览返回的 version"
// @Success	200		{object}	common.ApiResponse{data=model.VendorOperationResult}
// @Failure	400		{object}	common.ApiResponse	"请求无效或操作失败"
// @Failure	409		{object}	common.ApiResponse	"记录已变化或不存在时 code 为 VENDOR_CONFLICT；供应商仍被模型引用时 code 为 VENDOR_REFERENCED，reference_counts 为各供应商被引用的模型数"
// @Security	AdminAuth
// @Router		/api/vendors/operations [post]
func ApplyVendorOperation(c *gin.Context) {
	var request model.VendorOperation
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		vendorAPIError(c, err)
		return
	}
	result, err := model.ApplyVendorOperation(request)
	if err != nil {
		vendorAPIError(c, err)
		return
	}
	recordManageAudit(c, "vendor."+request.Action, map[string]any{"source_vendor_ids": request.VendorIDs, "target_vendor_id": request.TargetVendorID, "updated_model_ids": result.UpdatedModels, "deleted_vendor_ids": result.DeletedVendors})
	common.ApiSuccess(c, result)
}

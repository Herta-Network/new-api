package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/ionet"
	"github.com/gin-gonic/gin"
)

func getIoAPIKey(c *gin.Context) (string, bool) {
	common.OptionMapRWMutex.RLock()
	enabled := common.OptionMap["model_deployment.ionet.enabled"] == "true"
	apiKey := common.OptionMap["model_deployment.ionet.api_key"]
	common.OptionMapRWMutex.RUnlock()
	if !enabled || strings.TrimSpace(apiKey) == "" {
		common.ApiErrorMsg(c, "io.net model deployment is not enabled or api key missing")
		return "", false
	}
	return apiKey, true
}

type modelDeploymentSettings struct {
	Provider   string `json:"provider"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	CanConnect bool   `json:"can_connect"`
}

// @Summary	获取部署设置状态
// @Tags		部署
// @Success	200	{object}	common.ApiResponse{data=modelDeploymentSettings}
// @Security	AdminAuth
// @Router		/api/deployments/settings [get]
func GetModelDeploymentSettings(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	enabled := common.OptionMap["model_deployment.ionet.enabled"] == "true"
	hasAPIKey := strings.TrimSpace(common.OptionMap["model_deployment.ionet.api_key"]) != ""
	common.OptionMapRWMutex.RUnlock()

	common.ApiSuccess(c, modelDeploymentSettings{
		Provider:   "io.net",
		Enabled:    enabled,
		Configured: hasAPIKey,
		CanConnect: enabled && hasAPIKey,
	})
}

func getIoClient(c *gin.Context) (*ionet.Client, bool) {
	apiKey, ok := getIoAPIKey(c)
	if !ok {
		return nil, false
	}
	return ionet.NewClient(apiKey), true
}

func getIoEnterpriseClient(c *gin.Context) (*ionet.Client, bool) {
	apiKey, ok := getIoAPIKey(c)
	if !ok {
		return nil, false
	}
	return ionet.NewEnterpriseClient(apiKey), true
}

type ioNetConnectionTestRequest struct {
	APIKey string `json:"api_key"`
}

type ioNetConnectionTestResult struct {
	HardwareCount  int `json:"hardware_count"`
	TotalAvailable int `json:"total_available"`
}

// @Summary		测试 io.net 连接
// @Description	用 api_key（不传时用已保存的 API Key）查询 io.net 的可用硬件，不要求已启用部署。
// @Tags			部署
// @Param			body	body		ioNetConnectionTestRequest	false	"要测试的 API Key，可省略请求体"
// @Success		200		{object}	common.ApiResponse{data=ioNetConnectionTestResult}
// @Security		AdminAuth
// @Router			/api/deployments/settings/test-connection [post]
// @Router			/api/deployments/test-connection [post]
func TestIoNetConnection(c *gin.Context) {
	var req ioNetConnectionTestRequest

	rawBody, err := c.GetRawData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(bytes.TrimSpace(rawBody)) > 0 {
		if err := json.Unmarshal(rawBody, &req); err != nil {
			common.ApiErrorMsg(c, "invalid request payload")
			return
		}
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		common.OptionMapRWMutex.RLock()
		storedKey := strings.TrimSpace(common.OptionMap["model_deployment.ionet.api_key"])
		common.OptionMapRWMutex.RUnlock()
		if storedKey == "" {
			common.ApiErrorMsg(c, "api_key is required")
			return
		}
		apiKey = storedKey
	}

	client := ionet.NewEnterpriseClient(apiKey)
	result, err := client.GetMaxGPUsPerContainer()
	if err != nil {
		if apiErr, ok := err.(*ionet.APIError); ok {
			message := strings.TrimSpace(apiErr.Message)
			if message == "" {
				message = "failed to validate api key"
			}
			common.ApiErrorMsg(c, message)
			return
		}
		common.ApiError(c, err)
		return
	}

	totalHardware := 0
	totalAvailable := 0
	if result != nil {
		totalHardware = len(result.Hardware)
		totalAvailable = result.Total
		if totalAvailable == 0 {
			for _, hw := range result.Hardware {
				totalAvailable += hw.Available
			}
		}
	}

	common.ApiSuccess(c, ioNetConnectionTestResult{
		HardwareCount:  totalHardware,
		TotalAvailable: totalAvailable,
	})
}

func requireDeploymentID(c *gin.Context) (string, bool) {
	deploymentID := strings.TrimSpace(c.Param("id"))
	if deploymentID == "" {
		common.ApiErrorMsg(c, "deployment ID is required")
		return "", false
	}
	return deploymentID, true
}

func requireContainerID(c *gin.Context) (string, bool) {
	containerID := strings.TrimSpace(c.Param("container_id"))
	if containerID == "" {
		common.ApiErrorMsg(c, "container ID is required")
		return "", false
	}
	return containerID, true
}

type deploymentResourceConfig struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	GPU    string `json:"gpu"`
}

type ioNetDeploymentSummary struct {
	ID                      string                   `json:"id"`
	DeploymentName          string                   `json:"deployment_name"`
	ContainerName           string                   `json:"container_name"`
	Status                  string                   `json:"status"`
	Type                    string                   `json:"type"`
	TimeRemaining           string                   `json:"time_remaining"`
	TimeRemainingMinutes    int                      `json:"time_remaining_minutes"`
	HardwareInfo            string                   `json:"hardware_info"`
	HardwareName            string                   `json:"hardware_name"`
	BrandName               string                   `json:"brand_name"`
	HardwareQuantity        int                      `json:"hardware_quantity"`
	CompletedPercent        float64                  `json:"completed_percent"`
	ComputeMinutesServed    int                      `json:"compute_minutes_served"`
	ComputeMinutesRemaining int                      `json:"compute_minutes_remaining"`
	CreatedAt               int64                    `json:"created_at"`
	UpdatedAt               int64                    `json:"updated_at"`
	ModelName               string                   `json:"model_name"`
	ModelVersion            string                   `json:"model_version"`
	InstanceCount           int                      `json:"instance_count"`
	ResourceConfig          deploymentResourceConfig `json:"resource_config"`
	Description             string                   `json:"description"`
	Provider                string                   `json:"provider"`
}

func mapIoNetDeployment(d ionet.Deployment) ioNetDeploymentSummary {
	var created int64
	if d.CreatedAt.IsZero() {
		created = time.Now().Unix()
	} else {
		created = d.CreatedAt.Unix()
	}

	timeRemainingHours := d.ComputeMinutesRemaining / 60
	timeRemainingMins := d.ComputeMinutesRemaining % 60
	var timeRemaining string
	if timeRemainingHours > 0 {
		timeRemaining = fmt.Sprintf("%d hour %d minutes", timeRemainingHours, timeRemainingMins)
	} else if timeRemainingMins > 0 {
		timeRemaining = fmt.Sprintf("%d minutes", timeRemainingMins)
	} else {
		timeRemaining = "completed"
	}

	hardwareInfo := fmt.Sprintf("%s %s x%d", d.BrandName, d.HardwareName, d.HardwareQuantity)

	return ioNetDeploymentSummary{
		ID:                      d.ID,
		DeploymentName:          d.Name,
		ContainerName:           d.Name,
		Status:                  strings.ToLower(d.Status),
		Type:                    "Container",
		TimeRemaining:           timeRemaining,
		TimeRemainingMinutes:    d.ComputeMinutesRemaining,
		HardwareInfo:            hardwareInfo,
		HardwareName:            d.HardwareName,
		BrandName:               d.BrandName,
		HardwareQuantity:        d.HardwareQuantity,
		CompletedPercent:        d.CompletedPercent,
		ComputeMinutesServed:    d.ComputeMinutesServed,
		ComputeMinutesRemaining: d.ComputeMinutesRemaining,
		CreatedAt:               created,
		UpdatedAt:               created,
		ModelName:               "",
		ModelVersion:            "",
		InstanceCount:           d.HardwareQuantity,
		ResourceConfig: deploymentResourceConfig{
			CPU:    "",
			Memory: "",
			GPU:    strconv.Itoa(d.HardwareQuantity),
		},
		Description: "",
		Provider:    "io.net",
	}
}

func computeStatusCounts(total int, deployments []ionet.Deployment) map[string]int64 {
	counts := map[string]int64{
		"all": int64(total),
	}

	for _, status := range []string{"running", "completed", "failed", "deployment requested", "termination requested", "destroyed"} {
		counts[status] = 0
	}

	for _, d := range deployments {
		status := strings.ToLower(strings.TrimSpace(d.Status))
		counts[status] = counts[status] + 1
	}

	return counts
}

type deploymentListPage struct {
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Total    int                      `json:"total"`
	Items    []ioNetDeploymentSummary `json:"items"`
	// StatusCounts counts this page's deployments by status, plus "all",
	// the total of every page.
	StatusCounts map[string]int64 `json:"status_counts"`
}

// @Summary		列出部署
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			p			query		int		false	"页码，从 1 开始"
// @Param			page_size	query		int		false	"每页条数"
// @Param			status		query		string	false	"按状态筛选，如 running、completed、failed"
// @Success		200			{object}	common.ApiResponse{data=deploymentListPage}
// @Security		AdminAuth
// @Router			/api/deployments/ [get]
func GetAllDeployments(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	status := c.Query("status")
	opts := &ionet.ListDeploymentsOptions{
		Status:    strings.ToLower(strings.TrimSpace(status)),
		Page:      pageInfo.GetPage(),
		PageSize:  pageInfo.GetPageSize(),
		SortBy:    "created_at",
		SortOrder: "desc",
	}

	dl, err := client.ListDeployments(opts)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items := make([]ioNetDeploymentSummary, 0, len(dl.Deployments))
	for _, d := range dl.Deployments {
		items = append(items, mapIoNetDeployment(d))
	}

	data := deploymentListPage{
		Page:         pageInfo.GetPage(),
		PageSize:     pageInfo.GetPageSize(),
		Total:        dl.Total,
		Items:        items,
		StatusCounts: computeStatusCounts(dl.Total, dl.Deployments),
	}
	common.ApiSuccess(c, data)
}

type deploymentSearchPage struct {
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Total    int                      `json:"total"`
	Items    []ioNetDeploymentSummary `json:"items"`
}

// @Summary		搜索部署
// @Description	需已启用 io.net 部署并配置 API Key。keyword 只在当前页内按名称筛选，此时 total 是筛选后的条数。
// @Tags			部署
// @Param			p			query		int		false	"页码，从 1 开始"
// @Param			page_size	query		int		false	"每页条数"
// @Param			status		query		string	false	"按状态筛选"
// @Param			keyword		query		string	false	"名称关键字，不区分大小写"
// @Success		200			{object}	common.ApiResponse{data=deploymentSearchPage}
// @Security		AdminAuth
// @Router			/api/deployments/search [get]
func SearchDeployments(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	keyword := strings.TrimSpace(c.Query("keyword"))

	dl, err := client.ListDeployments(&ionet.ListDeploymentsOptions{
		Status:    status,
		Page:      pageInfo.GetPage(),
		PageSize:  pageInfo.GetPageSize(),
		SortBy:    "created_at",
		SortOrder: "desc",
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	filtered := make([]ionet.Deployment, 0, len(dl.Deployments))
	if keyword == "" {
		filtered = dl.Deployments
	} else {
		kw := strings.ToLower(keyword)
		for _, d := range dl.Deployments {
			if strings.Contains(strings.ToLower(d.Name), kw) {
				filtered = append(filtered, d)
			}
		}
	}

	items := make([]ioNetDeploymentSummary, 0, len(filtered))
	for _, d := range filtered {
		items = append(items, mapIoNetDeployment(d))
	}

	total := dl.Total
	if keyword != "" {
		total = len(filtered)
	}

	data := deploymentSearchPage{
		Page:     pageInfo.GetPage(),
		PageSize: pageInfo.GetPageSize(),
		Total:    total,
		Items:    items,
	}
	common.ApiSuccess(c, data)
}

type ioNetDeploymentDetail struct {
	ID                      string                          `json:"id"`
	DeploymentName          string                          `json:"deployment_name"`
	ModelName               string                          `json:"model_name"`
	ModelVersion            string                          `json:"model_version"`
	Status                  string                          `json:"status"`
	InstanceCount           int                             `json:"instance_count"`
	HardwareID              int                             `json:"hardware_id"`
	ResourceConfig          deploymentResourceConfig        `json:"resource_config"`
	CreatedAt               int64                           `json:"created_at"`
	UpdatedAt               int64                           `json:"updated_at"`
	Description             string                          `json:"description"`
	AmountPaid              float64                         `json:"amount_paid"`
	CompletedPercent        float64                         `json:"completed_percent"`
	GPUsPerContainer        int                             `json:"gpus_per_container"`
	TotalGPUs               int                             `json:"total_gpus"`
	TotalContainers         int                             `json:"total_containers"`
	HardwareName            string                          `json:"hardware_name"`
	BrandName               string                          `json:"brand_name"`
	ComputeMinutesServed    int                             `json:"compute_minutes_served"`
	ComputeMinutesRemaining int                             `json:"compute_minutes_remaining"`
	Locations               []ionet.DeploymentLocation      `json:"locations"`
	ContainerConfig         ionet.DeploymentContainerConfig `json:"container_config"`
}

// @Summary		获取部署详情
// @Description	需已启用 io.net 部署并配置 API Key。locations 和 container_config 原样来自 io.net。
// @Tags			部署
// @Param			id	path		string	true	"部署 ID"
// @Success		200	{object}	common.ApiResponse{data=ioNetDeploymentDetail}
// @Security		AdminAuth
// @Router			/api/deployments/{id} [get]
func GetDeployment(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	details, err := client.GetDeployment(deploymentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := ioNetDeploymentDetail{
		ID:             details.ID,
		DeploymentName: details.ID,
		ModelName:      "",
		ModelVersion:   "",
		Status:         strings.ToLower(details.Status),
		InstanceCount:  details.TotalContainers,
		HardwareID:     details.HardwareID,
		ResourceConfig: deploymentResourceConfig{
			CPU:    "",
			Memory: "",
			GPU:    strconv.Itoa(details.TotalGPUs),
		},
		CreatedAt:               details.CreatedAt.Unix(),
		UpdatedAt:               details.CreatedAt.Unix(),
		Description:             "",
		AmountPaid:              details.AmountPaid,
		CompletedPercent:        details.CompletedPercent,
		GPUsPerContainer:        details.GPUsPerContainer,
		TotalGPUs:               details.TotalGPUs,
		TotalContainers:         details.TotalContainers,
		HardwareName:            details.HardwareName,
		BrandName:               details.BrandName,
		ComputeMinutesServed:    details.ComputeMinutesServed,
		ComputeMinutesRemaining: details.ComputeMinutesRemaining,
		Locations:               details.Locations,
		ContainerConfig:         details.ContainerConfig,
	}

	common.ApiSuccess(c, data)
}

type updateDeploymentNameRequest struct {
	Name string `json:"name" binding:"required"`
}

type deploymentRename struct {
	// Status and Message are io.net's answer.
	Status  string `json:"status"`
	Message string `json:"message"`
	ID      string `json:"id"`
	Name    string `json:"name"`
}

// @Summary		重命名部署
// @Description	需已启用 io.net 部署并配置 API Key。名称已被占用时返回 success=false。
// @Tags			部署
// @Param			id		path		string						true	"部署 ID"
// @Param			body	body		updateDeploymentNameRequest	true	"新名称"
// @Success		200		{object}	common.ApiResponse{data=deploymentRename}
// @Security		AdminAuth
// @Router			/api/deployments/{id}/name [put]
func UpdateDeploymentName(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	var req updateDeploymentNameRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	updateReq := &ionet.UpdateClusterNameRequest{
		Name: strings.TrimSpace(req.Name),
	}

	if updateReq.Name == "" {
		common.ApiErrorMsg(c, "deployment name cannot be empty")
		return
	}

	available, err := client.CheckClusterNameAvailability(updateReq.Name)
	if err != nil {
		common.ApiError(c, fmt.Errorf("failed to check name availability: %w", err))
		return
	}

	if !available {
		common.ApiErrorMsg(c, "deployment name is not available, please choose a different name")
		return
	}

	resp, err := client.UpdateClusterName(deploymentID, updateReq)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := deploymentRename{
		Status:  resp.Status,
		Message: resp.Message,
		ID:      deploymentID,
		Name:    updateReq.Name,
	}
	common.ApiSuccess(c, data)
}

// @Summary		更新部署配置
// @Description	需已启用 io.net 部署并配置 API Key。请求转发给 io.net，data 是 io.net 返回的状态。
// @Tags			部署
// @Param			id		path		string							true	"部署 ID"
// @Param			body	body		ionet.UpdateDeploymentRequest	true	"要更新的容器配置"
// @Success		200		{object}	common.ApiResponse{data=ionet.UpdateDeploymentResponse}
// @Security		AdminAuth
// @Router			/api/deployments/{id} [put]
func UpdateDeployment(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	var req ionet.UpdateDeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	resp, err := client.UpdateDeployment(deploymentID, &req)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := ionet.UpdateDeploymentResponse{
		Status:       resp.Status,
		DeploymentID: resp.DeploymentID,
	}
	common.ApiSuccess(c, data)
}

// @Summary		延长部署时长
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			id		path		string						true	"部署 ID"
// @Param			body	body		ionet.ExtendDurationRequest	true	"延长的小时数"
// @Success		200		{object}	common.ApiResponse{data=ioNetDeploymentSummary}
// @Security		AdminAuth
// @Router			/api/deployments/{id}/extend [post]
func ExtendDeployment(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	var req ionet.ExtendDurationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	details, err := client.ExtendDeployment(deploymentID, &req)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := mapIoNetDeployment(ionet.Deployment{
		ID:                      details.ID,
		Status:                  details.Status,
		Name:                    deploymentID,
		CompletedPercent:        float64(details.CompletedPercent),
		HardwareQuantity:        details.TotalGPUs,
		BrandName:               details.BrandName,
		HardwareName:            details.HardwareName,
		ComputeMinutesServed:    details.ComputeMinutesServed,
		ComputeMinutesRemaining: details.ComputeMinutesRemaining,
		CreatedAt:               details.CreatedAt,
	})

	common.ApiSuccess(c, data)
}

type deploymentRequestResult struct {
	DeploymentID string `json:"deployment_id"`
	// Status is io.net's answer.
	Status  string `json:"status"`
	Message string `json:"message"`
}

// @Summary		终止部署
// @Description	需已启用 io.net 部署并配置 API Key。向 io.net 申请终止。
// @Tags			部署
// @Param			id	path		string	true	"部署 ID"
// @Success		200	{object}	common.ApiResponse{data=deploymentRequestResult}
// @Security		AdminAuth
// @Router			/api/deployments/{id} [delete]
func DeleteDeployment(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	resp, err := client.DeleteDeployment(deploymentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := deploymentRequestResult{
		Status:       resp.Status,
		DeploymentID: resp.DeploymentID,
		Message:      "Deployment termination requested successfully",
	}
	common.ApiSuccess(c, data)
}

// @Summary		创建部署
// @Description	需已启用 io.net 部署并配置 API Key。请求转发给 io.net 部署容器。
// @Tags			部署
// @Param			body	body		ionet.DeploymentRequest	true	"部署配置"
// @Success		200		{object}	common.ApiResponse{data=deploymentRequestResult}
// @Security		AdminAuth
// @Router			/api/deployments/ [post]
func CreateDeployment(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	var req ionet.DeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	resp, err := client.DeployContainer(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := deploymentRequestResult{
		DeploymentID: resp.DeploymentID,
		Status:       resp.Status,
		Message:      "Deployment created successfully",
	}
	common.ApiSuccess(c, data)
}

type hardwareTypeList struct {
	HardwareTypes  []ionet.HardwareType `json:"hardware_types"`
	Total          int                  `json:"total"`
	TotalAvailable int                  `json:"total_available"`
}

// @Summary		列出硬件类型
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Success		200	{object}	common.ApiResponse{data=hardwareTypeList}
// @Security		AdminAuth
// @Router			/api/deployments/hardware-types [get]
func GetHardwareTypes(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	hardwareTypes, totalAvailable, err := client.ListHardwareTypes()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := hardwareTypeList{
		HardwareTypes:  hardwareTypes,
		Total:          len(hardwareTypes),
		TotalAvailable: totalAvailable,
	}
	common.ApiSuccess(c, data)
}

// @Summary		列出部署地区
// @Description	需已启用 io.net 部署并配置 API Key。locations 原样来自 io.net（iso2 转为大写）。
// @Tags			部署
// @Success		200	{object}	common.ApiResponse{data=ionet.LocationsResponse}
// @Security		AdminAuth
// @Router			/api/deployments/locations [get]
func GetLocations(c *gin.Context) {
	client, ok := getIoClient(c)
	if !ok {
		return
	}

	locationsResp, err := client.ListLocations()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	total := locationsResp.Total
	if total == 0 {
		total = len(locationsResp.Locations)
	}

	data := ionet.LocationsResponse{
		Locations: locationsResp.Locations,
		Total:     total,
	}
	common.ApiSuccess(c, data)
}

// @Summary		查询可用副本数
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			hardware_id	query		int	true	"硬件 ID"
// @Param			gpu_count	query		int	false	"每个容器的 GPU 数，默认 1"
// @Success		200			{object}	common.ApiResponse{data=ionet.AvailableReplicasResponse}
// @Security		AdminAuth
// @Router			/api/deployments/available-replicas [get]
func GetAvailableReplicas(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	hardwareIDStr := c.Query("hardware_id")
	gpuCountStr := c.Query("gpu_count")

	if hardwareIDStr == "" {
		common.ApiErrorMsg(c, "hardware_id parameter is required")
		return
	}

	hardwareID, err := strconv.Atoi(hardwareIDStr)
	if err != nil || hardwareID <= 0 {
		common.ApiErrorMsg(c, "invalid hardware_id parameter")
		return
	}

	gpuCount := 1
	if gpuCountStr != "" {
		if parsed, err := strconv.Atoi(gpuCountStr); err == nil && parsed > 0 {
			gpuCount = parsed
		}
	}

	replicas, err := client.GetAvailableReplicas(hardwareID, gpuCount)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, replicas)
}

// @Summary		估算部署价格
// @Description	需已启用 io.net 部署并配置 API Key。请求转发给 io.net，data 是 io.net 的估价。
// @Tags			部署
// @Param			body	body		ionet.PriceEstimationRequest	true	"部署规格"
// @Success		200		{object}	common.ApiResponse{data=ionet.PriceEstimationResponse}
// @Security		AdminAuth
// @Router			/api/deployments/price-estimation [post]
func GetPriceEstimation(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	var req ionet.PriceEstimationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}

	priceResp, err := client.GetPriceEstimation(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, priceResp)
}

type deploymentNameAvailability struct {
	Available bool   `json:"available"`
	Name      string `json:"name"`
}

// @Summary		检查部署名称
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			name	query		string	true	"部署名称"
// @Success		200		{object}	common.ApiResponse{data=deploymentNameAvailability}
// @Security		AdminAuth
// @Router			/api/deployments/check-name [get]
func CheckClusterNameAvailability(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	clusterName := strings.TrimSpace(c.Query("name"))
	if clusterName == "" {
		common.ApiErrorMsg(c, "name parameter is required")
		return
	}

	available, err := client.CheckClusterNameAvailability(clusterName)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	data := deploymentNameAvailability{
		Available: available,
		Name:      clusterName,
	}
	common.ApiSuccess(c, data)
}

// @Summary		获取容器日志
// @Description	需已启用 io.net 部署并配置 API Key。data 是 io.net 返回的原始日志文本。
// @Tags			部署
// @Param			id				path		string	true	"部署 ID"
// @Param			container_id	query		string	true	"容器 ID"
// @Param			level			query		string	false	"日志级别"
// @Param			stream			query		string	false	"stdout 或 stderr"
// @Param			cursor			query		string	false	"分页游标"
// @Param			limit			query		int		false	"条数，默认 100，最大 1000"
// @Param			follow			query		bool	false	"持续跟随"
// @Param			start_time		query		string	false	"开始时间，RFC 3339"
// @Param			end_time		query		string	false	"结束时间，RFC 3339"
// @Success		200				{object}	common.ApiResponse{data=string}
// @Security		AdminAuth
// @Router			/api/deployments/{id}/logs [get]
func GetDeploymentLogs(c *gin.Context) {
	client, ok := getIoClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	containerID := c.Query("container_id")
	if containerID == "" {
		common.ApiErrorMsg(c, "container_id parameter is required")
		return
	}
	level := c.Query("level")
	stream := c.Query("stream")
	cursor := c.Query("cursor")
	limitStr := c.Query("limit")
	follow := c.Query("follow") == "true"

	var limit int = 100
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = min(parsedLimit, 1000)
		}
	}

	opts := &ionet.GetLogsOptions{
		Level:  level,
		Stream: stream,
		Limit:  limit,
		Cursor: cursor,
		Follow: follow,
	}

	if startTime := c.Query("start_time"); startTime != "" {
		if t, err := time.Parse(time.RFC3339, startTime); err == nil {
			opts.StartTime = &t
		}
	}
	if endTime := c.Query("end_time"); endTime != "" {
		if t, err := time.Parse(time.RFC3339, endTime); err == nil {
			opts.EndTime = &t
		}
	}

	rawLogs, err := client.GetContainerLogsRaw(deploymentID, containerID, opts)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, rawLogs)
}

type deploymentContainerEvent struct {
	Time    int64  `json:"time"`
	Message string `json:"message"`
}

type deploymentContainer struct {
	ContainerID      string                     `json:"container_id"`
	DeviceID         string                     `json:"device_id"`
	Status           string                     `json:"status"`
	Hardware         string                     `json:"hardware"`
	BrandName        string                     `json:"brand_name"`
	CreatedAt        int64                      `json:"created_at"`
	UptimePercent    int                        `json:"uptime_percent"`
	GPUsPerContainer int                        `json:"gpus_per_container"`
	PublicURL        string                     `json:"public_url"`
	Events           []deploymentContainerEvent `json:"events"`
}

type deploymentContainerList struct {
	Total      int                   `json:"total"`
	Containers []deploymentContainer `json:"containers"`
}

// @Summary		列出部署的容器
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			id	path		string	true	"部署 ID"
// @Success		200	{object}	common.ApiResponse{data=deploymentContainerList}
// @Security		AdminAuth
// @Router			/api/deployments/{id}/containers [get]
func ListDeploymentContainers(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	containers, err := client.ListContainers(deploymentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items := make([]deploymentContainer, 0)
	if containers != nil {
		items = make([]deploymentContainer, 0, len(containers.Workers))
		for _, ctr := range containers.Workers {
			events := make([]deploymentContainerEvent, 0, len(ctr.ContainerEvents))
			for _, event := range ctr.ContainerEvents {
				events = append(events, deploymentContainerEvent{
					Time:    event.Time.Unix(),
					Message: event.Message,
				})
			}

			items = append(items, deploymentContainer{
				ContainerID:      ctr.ContainerID,
				DeviceID:         ctr.DeviceID,
				Status:           strings.ToLower(strings.TrimSpace(ctr.Status)),
				Hardware:         ctr.Hardware,
				BrandName:        ctr.BrandName,
				CreatedAt:        ctr.CreatedAt.Unix(),
				UptimePercent:    ctr.UptimePercent,
				GPUsPerContainer: ctr.GPUsPerContainer,
				PublicURL:        ctr.PublicURL,
				Events:           events,
			})
		}
	}

	response := deploymentContainerList{
		Total:      0,
		Containers: items,
	}
	if containers != nil {
		response.Total = containers.Total
	}

	common.ApiSuccess(c, response)
}

type deploymentContainerDetail struct {
	DeploymentID string `json:"deployment_id"`
	deploymentContainer
}

// @Summary		获取容器详情
// @Description	需已启用 io.net 部署并配置 API Key。
// @Tags			部署
// @Param			id				path		string	true	"部署 ID"
// @Param			container_id	path		string	true	"容器 ID"
// @Success		200				{object}	common.ApiResponse{data=deploymentContainerDetail}
// @Security		AdminAuth
// @Router			/api/deployments/{id}/containers/{container_id} [get]
func GetContainerDetails(c *gin.Context) {
	client, ok := getIoEnterpriseClient(c)
	if !ok {
		return
	}

	deploymentID, ok := requireDeploymentID(c)
	if !ok {
		return
	}

	containerID, ok := requireContainerID(c)
	if !ok {
		return
	}

	details, err := client.GetContainerDetails(deploymentID, containerID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if details == nil {
		common.ApiErrorMsg(c, "container details not found")
		return
	}

	events := make([]deploymentContainerEvent, 0, len(details.ContainerEvents))
	for _, event := range details.ContainerEvents {
		events = append(events, deploymentContainerEvent{
			Time:    event.Time.Unix(),
			Message: event.Message,
		})
	}

	data := deploymentContainerDetail{
		DeploymentID: deploymentID,
		deploymentContainer: deploymentContainer{
			ContainerID:      details.ContainerID,
			DeviceID:         details.DeviceID,
			Status:           strings.ToLower(strings.TrimSpace(details.Status)),
			Hardware:         details.Hardware,
			BrandName:        details.BrandName,
			CreatedAt:        details.CreatedAt.Unix(),
			UptimePercent:    details.UptimePercent,
			GPUsPerContainer: details.GPUsPerContainer,
			PublicURL:        details.PublicURL,
			Events:           events,
		},
	}

	common.ApiSuccess(c, data)
}

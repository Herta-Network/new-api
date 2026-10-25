package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxTaskPluginSourceBytes = 8 << 20

func taskPluginCompileError(c *gin.Context, err error) {
	var unknownField *jsplugin.UnknownMetaFieldError
	if errors.As(err, &unknownField) {
		common.ApiErrorT(c, "Plugin metadata contains an unknown field \"{{field}}\". If this plugin was downloaded from the official marketplace, it may require a newer version of new-api. Try updating new-api and installing the plugin again.", map[string]any{"field": unknownField.Field})
		return
	}
	common.ApiError(c, err)
}

type taskPluginUploadRequest struct {
	Source       string `json:"source" binding:"required"`
	Enabled      *bool  `json:"enabled"`
	Remark       string `json:"remark"`
	Force        bool   `json:"force"`
	SourceSha256 string `json:"sourceSha256"`
	// Icon carries the sidecar icon.svg / icon.png as a data URI. It is optional
	// and stored separately from the source so the JavaScript stays readable.
	Icon string `json:"icon"`
}

// @Summary		上传任务插件
// @Description	校验源码（不超过 8 MiB）并保存为覆盖层的一个版本；该插件尚无激活版本时自动激活。enabled 默认 true；启用时与已有插件的路由冲突会被拒绝，force 跳过此检查。同一版本重复上传时只更新 enabled、remark 和图标，源码不同则失败。
// @Tags			任务插件
// @Param			body	body		taskPluginUploadRequest	true	"插件源码及选项"
// @Success		200		{object}	common.ApiResponse{data=taskPluginDetail}
// @Security		RootAuth
// @Router			/api/plugin/task [post]
// @Router			/api/plugin/task [put]
func UploadTaskPlugin(c *gin.Context) {
	var request taskPluginUploadRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if len(request.Source) > maxTaskPluginSourceBytes {
		common.ApiErrorMsg(c, "plugin source exceeds 8 MiB")
		return
	}
	if expected := strings.TrimSpace(request.SourceSha256); expected != "" {
		actual := fmt.Sprintf("%x", sha256.Sum256([]byte(request.Source)))
		if !strings.EqualFold(actual, expected) {
			common.ApiErrorMsg(c, "plugin source sha256 mismatch")
			return
		}
	}
	temporary := jsplugin.NewRegistry()
	loaded, err := temporary.Register(request.Source, jsplugin.Options{})
	if err != nil {
		taskPluginCompileError(c, err)
		return
	}
	if err = jsplugin.ValidateV1Meta(loaded.Meta); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	icon := strings.TrimSpace(request.Icon)
	if icon != "" {
		if _, _, err = jsplugin.DecodeIconDataURI(icon); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	if enabled && !request.Force {
		if err = jsplugin.PreflightRoutingConflict(jsplugin.DefaultRegistry.Generation(), loaded); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	plugin := model.TaskPlugin{
		Key: loaded.Meta.Key, APIVersion: loaded.Meta.APIVersion, Version: loaded.Meta.Version,
		Source: model.LongText(request.Source), SourceHash: fmt.Sprintf("%x", sha256.Sum256([]byte(request.Source))),
		Icon: model.LongText(icon), Enabled: enabled, Remark: request.Remark,
	}
	if err = model.SaveTaskPlugin(&plugin); err != nil {
		common.ApiError(c, err)
		return
	}
	if err = syncTaskPluginsOnceContext(c.Request.Context()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, taskPluginDetail{Plugin: &plugin, Meta: loaded.Meta, Source: string(plugin.Source), Layer: "override", HasIcon: plugin.HasIcon()})
}

// @Summary	列出任务插件版本
// @Tags		任务插件
// @Param		key	path		string	true	"插件 key"
// @Success	200	{object}	common.ApiResponse{data=[]model.TaskPlugin}
// @Security	RootAuth
// @Router		/api/plugin/task/{key}/versions [get]
func GetTaskPluginVersions(c *gin.Context) {
	plugins, err := model.ListTaskPluginVersions(c.Param("key"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, plugins)
}

type taskPluginListItem struct {
	Meta          jsplugin.Meta  `json:"meta"`
	Source        string         `json:"source"`
	Enabled       bool           `json:"enabled"`
	Active        bool           `json:"active"`
	SourceHash    string         `json:"source_hash"`
	HasIcon       bool           `json:"has_icon"`
	Remark        string         `json:"remark"`
	RuntimeStatus string         `json:"runtime_status"`
	RuntimeError  string         `json:"runtime_error,omitempty"`
	FactoryMeta   *jsplugin.Meta `json:"factory_meta,omitempty"`
	ChannelCount  int            `json:"channel_count"`
	InFlightCount int64          `json:"in_flight_count"`
}

type taskPluginRebuildOutcome struct {
	Status           string    `json:"status"`
	AttemptedAt      time.Time `json:"attempted_at"`
	Generation       uint64    `json:"generation"`
	DatabaseRevision string    `json:"database_revision,omitempty"`
	PluginErrorCount int       `json:"plugin_error_count"`
	Error            string    `json:"error,omitempty"`
}

type taskPluginRuntimeStatus struct {
	CurrentGeneration     uint64                   `json:"current_generation"`
	GenerationPublishedAt time.Time                `json:"generation_published_at"`
	DatabaseRevision      string                   `json:"database_revision"`
	DatabaseError         string                   `json:"database_error,omitempty"`
	LastRebuild           taskPluginRebuildOutcome `json:"last_rebuild"`
	PluginErrors          map[string]string        `json:"plugin_errors"`
}

// @Summary		列出任务插件
// @Description	合并内置插件和覆盖层插件；source 是 factory、override 或 override_over_factory，runtime_status 是 registered、compile_failed、not_registered、disabled_fallback 或 disabled。
// @Tags			任务插件
// @Success		200	{object}	common.ApiResponse{data=[]taskPluginListItem}
// @Security		RootAuth
// @Router			/api/plugin/task [get]
func ListTaskPlugins(c *gin.Context) {
	databasePlugins, err := model.ListTaskPlugins()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	snapshot := jsplugin.DefaultRegistry.Snapshot()
	factory := make(map[string]jsplugin.Meta, len(snapshot.Factory))
	override := make(map[string]jsplugin.Meta, len(snapshot.Override))
	for _, meta := range snapshot.Factory {
		factory[meta.Key] = meta
	}
	for _, meta := range snapshot.Override {
		override[meta.Key] = meta
	}
	activeRows := make(map[string]model.TaskPlugin)
	keys := make(map[string]struct{}, len(factory)+len(databasePlugins))
	for key := range factory {
		keys[key] = struct{}{}
	}
	for _, plugin := range databasePlugins {
		keys[plugin.Key] = struct{}{}
		if plugin.Active {
			activeRows[plugin.Key] = plugin
		}
	}

	runtimeErrors := jsplugin.DefaultRegistry.RoutingErrors()
	taskPluginSyncState.Lock()
	maps.Copy(runtimeErrors, taskPluginSyncState.errors)
	taskPluginSyncState.Unlock()

	items := make([]taskPluginListItem, 0, len(keys))
	for key := range keys {
		factoryMeta, hasFactory := factory[key]
		row, hasOverride := activeRows[key]
		item := taskPluginListItem{Enabled: true, Active: true, RuntimeStatus: "registered"}
		if hasOverride {
			item.Source = "override"
			if hasFactory {
				item.Source = "override_over_factory"
				factoryCopy := factoryMeta
				item.FactoryMeta = &factoryCopy
			}
			item.Meta = jsplugin.Meta{Key: row.Key, Version: row.Version, APIVersion: row.APIVersion}
			if compiled, compileErr := jsplugin.NewRegistry().Register(string(row.Source), jsplugin.Options{Key: row.Key, Version: row.Version}); compileErr == nil {
				item.Meta = compiled.Meta
			}
			item.Enabled = row.Enabled
			item.Active = row.Active
			item.SourceHash = row.SourceHash
			item.HasIcon = row.HasIcon()
			item.Remark = row.Remark
			if message := runtimeErrors[key]; message != "" {
				item.RuntimeStatus = "compile_failed"
				item.RuntimeError = message
			} else if runtimeMeta, ok := override[key]; ok {
				item.Meta = runtimeMeta
			} else if !row.Enabled {
				item.RuntimeStatus = "disabled_fallback"
			} else {
				item.RuntimeStatus = "not_registered"
			}
			// "disabled_fallback" promises that the built-in still serves. When
			// the factory layer is suppressed as well, nothing serves this key.
			if item.RuntimeStatus == "disabled_fallback" && hasFactory && setting.IsTaskPluginFactoryDisabled(key) {
				item.RuntimeStatus = "disabled"
			}
		} else {
			item.Source = "factory"
			item.Meta = factoryMeta
			item.Enabled = !setting.IsTaskPluginFactoryDisabled(key)
			_, _, item.HasIcon = plugins.Icon(key)
			source, sourceErr := plugins.Source(key)
			if sourceErr == nil {
				item.SourceHash = fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
			}
			if !item.Enabled {
				item.RuntimeStatus = "disabled"
			} else if message := runtimeErrors[key]; message != "" {
				item.RuntimeStatus = "compile_failed"
				item.RuntimeError = message
			}
		}
		if !hasFactory {
			channels, inFlight, usageErr := model.GetTaskPluginUsage(key)
			if usageErr != nil {
				common.ApiError(c, usageErr)
				return
			}
			item.ChannelCount = len(channels)
			item.InFlightCount = inFlight
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Meta.SortPriority != items[j].Meta.SortPriority {
			return items[i].Meta.SortPriority > items[j].Meta.SortPriority
		}
		return items[i].Meta.Key < items[j].Meta.Key
	})
	common.ApiSuccess(c, items)
}

// @Summary		获取任务插件运行状态
// @Description	last_rebuild.status 是 success、partial、failed 或 never。
// @Tags			任务插件
// @Success		200	{object}	common.ApiResponse{data=taskPluginRuntimeStatus}
// @Security		RootAuth
// @Router			/api/plugin/task/runtime/status [get]
func GetTaskPluginRuntime(c *gin.Context) {
	routingStatus := jsplugin.DefaultRegistry.RoutingStatus()
	pluginErrors := routingStatus.Errors

	taskPluginSyncState.Lock()
	maps.Copy(pluginErrors, taskPluginSyncState.errors)
	lastRebuild := taskPluginSyncState.lastRebuild
	lastDatabaseRevision := lastRebuild.DatabaseRevision
	taskPluginSyncState.Unlock()

	registryRebuild := routingStatus.LastRebuild
	if lastRebuild.AttemptedAt.Before(registryRebuild.AttemptedAt) {
		lastRebuild = taskPluginRebuildOutcome{
			Status:      registryRebuild.Status,
			AttemptedAt: registryRebuild.AttemptedAt,
			Generation:  registryRebuild.Generation,
			Error:       registryRebuild.Error,
		}
	}
	if lastRebuild.Status == "" {
		lastRebuild.Status = "never"
	}
	lastRebuild.PluginErrorCount = len(pluginErrors)
	if lastRebuild.Status == "success" && len(pluginErrors) > 0 {
		lastRebuild.Status = "partial"
	}

	status := taskPluginRuntimeStatus{
		DatabaseRevision: lastDatabaseRevision,
		LastRebuild:      lastRebuild,
		PluginErrors:     pluginErrors,
	}
	databaseSnapshot, err := model.GetTaskPluginSyncSnapshot()
	if err != nil {
		status.DatabaseError = "database snapshot unavailable"
	} else {
		status.DatabaseRevision = databaseSnapshot.Revision
	}
	if routingStatus.Generation != nil {
		status.CurrentGeneration = routingStatus.Generation.Number
		status.GenerationPublishedAt = routingStatus.Generation.PublishedAt
	}
	common.ApiSuccess(c, status)
}

type taskPluginDetail struct {
	Plugin  *model.TaskPlugin `json:"plugin,omitempty"`
	Meta    jsplugin.Meta     `json:"meta"`
	Source  string            `json:"source"`
	Layer   string            `json:"layer"`
	HasIcon bool              `json:"has_icon"`
}

// GetTaskPluginIcon serves a plugin logo as an image. The active override wins
// (or the requested ?version=), then the factory sidecar. Data icons are only
// ever drawn through <img>, and the nosniff header keeps a browser from
// treating an SVG response as a document.
//
//	@Summary	获取任务插件图标
//	@Tags		任务插件
//	@Produce	png,image/svg+xml
//	@Param		key		path	string	true	"插件 key"
//	@Param		version	query	string	false	"覆盖层版本，不传时取激活版本，再取内置插件的图标"
//	@Success	200		{file}	file
//	@Failure	404		"没有图标"
//	@Security	RootAuth
//	@Router		/api/plugin/task/{key}/icon [get]
func GetTaskPluginIcon(c *gin.Context) {
	key := c.Param("key")
	icon := ""
	plugin, err := model.GetTaskPluginVersion(key, c.Query("version"))
	if err == nil {
		icon = string(plugin.Icon)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiError(c, err)
		return
	}
	if icon == "" && c.Query("version") == "" {
		icon = plugins.IconDataURI(key)
	}
	if icon == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	mediaType, data, err := jsplugin.DecodeIconDataURI(icon)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	c.Data(http.StatusOK, mediaType, data)
}

// @Summary		获取任务插件
// @Description	不传 version 时返回激活的覆盖层版本，没有则返回内置插件（layer 为 factory，无 plugin）。
// @Tags			任务插件
// @Param			key		path		string	true	"插件 key"
// @Param			version	query		string	false	"覆盖层版本"
// @Success		200		{object}	common.ApiResponse{data=taskPluginDetail}
// @Security		RootAuth
// @Router			/api/plugin/task/{key} [get]
func GetTaskPlugin(c *gin.Context) {
	key := c.Param("key")
	version := c.Query("version")
	plugin, err := model.GetTaskPluginVersion(key, version)
	if err == nil {
		loaded, compileErr := jsplugin.NewRegistry().Register(string(plugin.Source), jsplugin.Options{Key: plugin.Key, Version: plugin.Version})
		if compileErr != nil {
			taskPluginCompileError(c, compileErr)
			return
		}
		common.ApiSuccess(c, taskPluginDetail{Plugin: plugin, Meta: loaded.Meta, Source: string(plugin.Source), Layer: "override", HasIcon: plugin.HasIcon()})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) || version != "" {
		common.ApiError(c, err)
		return
	}
	source, err := plugins.Source(key)
	if err != nil {
		common.ApiErrorMsg(c, "task plugin not found")
		return
	}
	loaded, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: key})
	if err != nil {
		taskPluginCompileError(c, err)
		return
	}
	_, _, hasIcon := plugins.Icon(key)
	common.ApiSuccess(c, taskPluginDetail{Meta: loaded.Meta, Source: source, Layer: "factory", HasIcon: hasIcon})
}

type taskPluginDryRunRequest struct {
	Hook   string            `json:"hook" binding:"required"`
	Member string            `json:"member"`
	Args   []json.RawMessage `json:"args" swaggertype:"array,object"`
}

// @Summary		试运行任务插件钩子
// @Description	用激活的覆盖层版本（没有则用内置插件）调用 hook（或 hook 的成员 member），data 是钩子的返回值。
// @Tags			任务插件
// @Param			key		path		string					true	"插件 key"
// @Param			body	body		taskPluginDryRunRequest	true	"钩子名和参数"
// @Success		200		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/plugin/task/{key}/dryrun [post]
func DryRunTaskPlugin(c *gin.Context) {
	var request taskPluginDryRunRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	detailSource := ""
	plugin, err := model.GetTaskPluginVersion(c.Param("key"), "")
	if err == nil {
		detailSource = string(plugin.Source)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		detailSource, err = plugins.Source(c.Param("key"))
	}
	if err != nil {
		common.ApiErrorMsg(c, "task plugin not found")
		return
	}
	loaded, err := jsplugin.NewRegistry().Register(detailSource, jsplugin.Options{Key: c.Param("key")})
	if err != nil {
		taskPluginCompileError(c, err)
		return
	}
	args := make([]any, len(request.Args))
	for index, raw := range request.Args {
		if err = common.Unmarshal(raw, &args[index]); err != nil {
			common.ApiErrorMsg(c, fmt.Sprintf("invalid argument %d: %v", index+1, err))
			return
		}
	}
	var output any
	if request.Member == "" {
		output, err = loaded.Engine.Call(context.Background(), request.Hook, args...)
	} else {
		output, err = loaded.Engine.CallMember(context.Background(), request.Hook, request.Member, args...)
	}
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, output)
}

// @Summary		删除任务插件版本
// @Description	只能删除覆盖层版本。删除没有内置插件的激活版本时，若仍有渠道绑定或进行中的任务且未传 force=true，返回 success=false，data 含 channels 和 in_flight_count。
// @Tags			任务插件
// @Param			key		path		string	true	"插件 key"
// @Param			version	path		string	true	"版本"
// @Param			force	query		bool	false	"仍在使用时也删除"
// @Success		200		{object}	common.ApiResponse
// @Security		RootAuth
// @Router			/api/plugin/task/{key}/versions/{version} [delete]
func DeleteTaskPluginVersion(c *gin.Context) {
	key := c.Param("key")
	version := c.Param("version")
	plugin, lookupErr := model.GetTaskPluginVersion(key, version)
	if lookupErr != nil {
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "override plugin version not found; factory plugins cannot be deleted")
			return
		}
		common.ApiError(c, lookupErr)
		return
	}
	if plugin.Active && !taskPluginHasFactory(key) {
		channels, inFlight, usageErr := model.GetTaskPluginUsage(key)
		if usageErr != nil {
			common.ApiError(c, usageErr)
			return
		}
		if (len(channels) > 0 || inFlight > 0) && c.Query("force") != "true" {
			c.JSON(200, gin.H{"success": false, "message": "task plugin is still in use", "data": gin.H{"channels": channels, "in_flight_count": inFlight}})
			return
		}
	}
	_, err := model.DeleteTaskPluginVersion(key, version)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "override plugin version not found; factory plugins cannot be deleted")
			return
		}
		common.ApiError(c, err)
		return
	}
	if err = syncTaskPluginsOnceContext(c.Request.Context()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

type taskPluginActivateRequest struct {
	Version string `json:"version" binding:"required"`
}

// @Summary	激活任务插件版本
// @Tags		任务插件
// @Param		key		path		string						true	"插件 key"
// @Param		body	body		taskPluginActivateRequest	true	"要激活的版本"
// @Success	200		{object}	common.ApiResponse
// @Security	RootAuth
// @Router		/api/plugin/task/{key}/activate [post]
func ActivateTaskPlugin(c *gin.Context) {
	var request taskPluginActivateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	versions, err := model.ListTaskPluginVersions(c.Param("key"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var target *model.TaskPlugin
	for i := range versions {
		if versions[i].Version == request.Version {
			target = &versions[i]
			break
		}
	}
	if target == nil {
		common.ApiErrorMsg(c, "plugin version not found")
		return
	}
	if _, err = jsplugin.NewRegistry().Register(string(target.Source), jsplugin.Options{Key: target.Key, Version: target.Version}); err != nil {
		taskPluginCompileError(c, err)
		return
	}
	if err = model.ActivateTaskPlugin(target.Key, target.Version); err != nil {
		common.ApiError(c, err)
		return
	}
	if err = syncTaskPluginsOnceContext(c.Request.Context()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

type taskPluginStatusRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type taskPluginStatusResult struct {
	PluginEnabled    bool `json:"plugin_enabled"`
	DisabledChannels int  `json:"disabled_channels"`
	UnboundChannels  int  `json:"unbound_channels"`
}

// @Summary		启用或停用任务插件
// @Description	同时作用于内置插件和覆盖层。停用时若有渠道绑定且未传 cascade=true，或有进行中的任务且未传 force=true，返回 success=false，data 含 channels 和 in_flight_count。cascade 停用绑定的渠道，New API 类型的渠道只解除绑定。
// @Tags			任务插件
// @Param			key		path		string					true	"插件 key"
// @Param			cascade	query		bool					false	"停用时一并处理绑定的渠道"
// @Param			force	query		bool					false	"有进行中的任务时也停用"
// @Param			body	body		taskPluginStatusRequest	true	"是否启用"
// @Success		200		{object}	common.ApiResponse{data=taskPluginStatusResult}
// @Security		RootAuth
// @Router			/api/plugin/task/{key}/status [post]
func SetTaskPluginStatus(c *gin.Context) {
	var request taskPluginStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Enabled == nil {
		common.ApiErrorMsg(c, "enabled is required")
		return
	}
	key := c.Param("key")
	disabledChannels := 0
	unboundChannels := 0
	if !*request.Enabled {
		channels, inFlight, usageErr := model.GetTaskPluginUsage(key)
		if usageErr != nil {
			common.ApiError(c, usageErr)
			return
		}
		cascade := c.Query("cascade") == "true"
		force := c.Query("force") == "true"
		if (len(channels) > 0 && !cascade) || (inFlight > 0 && !force) {
			c.JSON(200, gin.H{"success": false, "message": "task plugin is still in use", "data": gin.H{"channels": channels, "in_flight_count": inFlight}})
			return
		}
		if cascade {
			for _, channel := range channels {
				if channel.Type == constant.ChannelTypeNewAPI {
					// A gateway channel also carries ordinary traffic, so the
					// cascade only drops this plugin from its bindings.
					unbound, unbindErr := model.UnbindTaskPlugin(channel.Id, key)
					if unbindErr != nil {
						common.ApiError(c, unbindErr)
						return
					}
					if unbound {
						unboundChannels++
					}
					continue
				}
				if model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "task plugin disabled") {
					disabledChannels++
				}
			}
			if unboundChannels > 0 {
				model.InitChannelCache()
			}
		}
	}
	_, lookupErr := model.GetTaskPluginVersion(key, "")
	hasActiveOverride := lookupErr == nil
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		common.ApiError(c, lookupErr)
		return
	}
	// Switching a key off must silence every layer that can serve it. The
	// factory built-in goes into the disabled set even when an override row
	// exists; otherwise the built-in would keep routing the same models (and
	// blocking same-model uploads) right after the administrator disabled the
	// plugin. Switching on reverses both layers.
	if taskPluginHasFactory(key) {
		keys := setting.GetTaskPluginDisabledFactoryKeys()
		if *request.Enabled {
			next := make([]string, 0, len(keys))
			for _, item := range keys {
				if item != key {
					next = append(next, item)
				}
			}
			keys = next
		} else {
			keys = append(append([]string{}, keys...), key)
		}
		if err := setting.SetTaskPluginDisabledFactoryKeysOption(keys); err != nil {
			common.ApiError(c, err)
			return
		}
		encoded, err := common.Marshal(setting.GetTaskPluginDisabledFactoryKeys())
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err = model.UpdateOption(setting.TaskPluginDisabledFactoryKeysKey, string(encoded)); err != nil {
			common.ApiError(c, err)
			return
		}
		if !hasActiveOverride {
			common.ApiSuccess(c, taskPluginStatusResult{PluginEnabled: *request.Enabled, DisabledChannels: disabledChannels, UnboundChannels: unboundChannels})
			return
		}
	}
	if err := model.SetTaskPluginEnabled(key, *request.Enabled); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := syncTaskPluginsOnceContext(c.Request.Context()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, taskPluginStatusResult{PluginEnabled: *request.Enabled, DisabledChannels: disabledChannels, UnboundChannels: unboundChannels})
}

func taskPluginHasFactory(key string) bool {
	for _, meta := range jsplugin.DefaultRegistry.Snapshot().Factory {
		if meta.Key == key {
			return true
		}
	}
	return false
}

// @Summary	获取插件市场源
// @Tags		任务插件
// @Success	200	{object}	common.ApiResponse{data=[]setting.TaskPluginMarketplaceSource}
// @Security	RootAuth
// @Router		/api/plugin/task/marketplace/sources [get]
func GetTaskPluginMarketplaceSources(c *gin.Context) {
	common.ApiSuccess(c, setting.GetTaskPluginMarketplaceSources())
}

// @Summary		更新插件市场源
// @Description	整体替换；每项需要 name 和绝对 http(s) 地址 index_url。返回保存后的列表。
// @Tags			任务插件
// @Param			body	body		[]setting.TaskPluginMarketplaceSource	true	"市场源列表"
// @Success		200		{object}	common.ApiResponse{data=[]setting.TaskPluginMarketplaceSource}
// @Security		RootAuth
// @Router			/api/plugin/task/marketplace/sources [put]
func UpdateTaskPluginMarketplaceSources(c *gin.Context) {
	var sources []setting.TaskPluginMarketplaceSource
	if err := c.ShouldBindJSON(&sources); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if sources == nil {
		sources = []setting.TaskPluginMarketplaceSource{}
	}
	for i := range sources {
		name := strings.TrimSpace(sources[i].Name)
		indexURL := strings.TrimSpace(sources[i].IndexURL)
		if name == "" {
			common.ApiErrorMsg(c, "marketplace source name is required")
			return
		}
		parsed, err := url.Parse(indexURL)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
			common.ApiErrorMsg(c, "marketplace source index_url must be an absolute http(s) URL")
			return
		}
		sources[i].Name = name
		sources[i].IndexURL = indexURL
	}
	encoded, err := common.Marshal(sources)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.UpdateOption(setting.TaskPluginMarketplaceSourcesKey, string(encoded)); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, sources)
}

type taskPluginOption struct {
	Key           string                               `json:"key"`
	Name          string                               `json:"name"`
	Description   jsplugin.LocalizedText               `json:"description"`
	Icon          string                               `json:"icon"`
	HasIcon       bool                                 `json:"hasIcon"`
	BaseURL       string                               `json:"baseUrl"`
	SortPriority  int                                  `json:"sortPriority"`
	Website       string                               `json:"website"`
	Models        []string                             `json:"models"`
	ChannelTypes  []int                                `json:"channelTypes"`
	Upstreams     []string                             `json:"upstreams"`
	UsageSchema   map[string]jsplugin.UsageFieldSchema `json:"usageSchema"`
	UsageProfiles []jsplugin.UsageProfile              `json:"usageProfiles"`
}

// @Summary		列出可绑定的任务插件
// @Description	需要 task_plugin.bind 权限。列出已注册、可绑定到渠道的插件，已停用的内置插件除外。
// @Tags			任务插件
// @Success		200	{object}	common.ApiResponse{data=[]taskPluginOption}
// @Security		AdminAuth
// @Router			/api/task_plugin_options [get]
func GetTaskPluginOptions(c *gin.Context) {
	snapshot := jsplugin.DefaultRegistry.Snapshot()
	seen := make(map[string]bool)
	options := make([]taskPluginOption, 0, len(snapshot.Factory)+len(snapshot.Override))
	for layer, metas := range [][]jsplugin.Meta{snapshot.Override, snapshot.Factory} {
		for _, meta := range metas {
			if seen[meta.Key] {
				continue
			}
			// Disabled factory keys are omitted from bind options. The disabled
			// set suppresses only the factory fallback; an enabled override for
			// the same key is listed in the override pass and still appears.
			if layer == 1 && setting.IsTaskPluginFactoryDisabled(meta.Key) {
				continue
			}
			if _, ok := jsplugin.DefaultRegistry.Get(meta.Key); !ok {
				continue
			}
			seen[meta.Key] = true
			hasIcon := false
			if layer == 0 {
				if row, rowErr := model.GetTaskPluginVersion(meta.Key, ""); rowErr == nil {
					hasIcon = row.HasIcon()
				}
			} else {
				_, _, hasIcon = plugins.Icon(meta.Key)
			}
			options = append(options, taskPluginOption{
				Key:           meta.Key,
				Name:          meta.Name,
				Description:   meta.Description,
				Icon:          meta.Icon,
				HasIcon:       hasIcon,
				BaseURL:       meta.BaseURL,
				SortPriority:  meta.SortPriority,
				Website:       meta.Website,
				Models:        meta.Models,
				ChannelTypes:  meta.ChannelTypes,
				Upstreams:     meta.Upstreams,
				UsageSchema:   meta.UsageSchema,
				UsageProfiles: meta.UsageProfiles,
			})
		}
	}
	sort.Slice(options, func(i, j int) bool {
		left, right := options[i].SortPriority, options[j].SortPriority
		if left != right {
			return left > right
		}
		return options[i].Key < options[j].Key
	})
	common.ApiSuccess(c, options)
}

var taskPluginSyncState = struct {
	sync.Mutex
	hashes      map[string]string
	errors      map[string]string
	lastRebuild taskPluginRebuildOutcome
}{hashes: map[string]string{}, errors: map[string]string{}}

func syncTaskPluginsOnce() error {
	return syncTaskPluginsOnceContext(context.Background())
}

func syncTaskPluginsOnceContext(ctx context.Context) error {
	started := time.Now()
	taskPluginSyncState.Lock()
	defer taskPluginSyncState.Unlock()
	databaseSnapshot, err := model.GetTaskPluginSyncSnapshot()
	if err != nil {
		syncErr := fmt.Errorf("sync task plugins: %w", err)
		taskPluginSyncState.lastRebuild = taskPluginRebuildOutcome{
			Status:           "failed",
			AttemptedAt:      time.Now(),
			Generation:       jsplugin.DefaultRegistry.Generation().Number,
			DatabaseRevision: taskPluginSyncState.lastRebuild.DatabaseRevision,
			Error:            syncErr.Error(),
		}
		logger.LogDebug(
			ctx,
			"task_plugin subsystem=sync event=failed stage=database_snapshot retained_generation=%d elapsed_ms=%d",
			jsplugin.DefaultRegistry.Generation().Number,
			time.Since(started).Milliseconds(),
		)
		return syncErr
	}
	databasePlugins := databaseSnapshot.Plugins
	sort.Slice(databasePlugins, func(i, j int) bool { return databasePlugins[i].Key < databasePlugins[j].Key })
	currentOverrides := jsplugin.DefaultRegistry.OverridePlugins()
	generationBefore := jsplugin.DefaultRegistry.Generation().Number
	logger.LogDebug(
		ctx,
		"task_plugin subsystem=sync event=start database_revision=%q generation=%d desired_plugins=%d current_overrides=%d",
		databaseSnapshot.Revision,
		generationBefore,
		len(databasePlugins),
		len(currentOverrides),
	)
	nextOverrides := make([]*jsplugin.LoadedPlugin, 0, len(databasePlugins))
	nextHashes := make(map[string]string, len(databasePlugins))
	seen := make(map[string]bool, len(databasePlugins))
	for _, plugin := range databasePlugins {
		seen[plugin.Key] = true
		if current := currentOverrides[plugin.Key]; current != nil && taskPluginSyncState.hashes[plugin.Key] == plugin.SourceHash {
			nextOverrides = append(nextOverrides, current)
			nextHashes[plugin.Key] = plugin.SourceHash
			logger.LogDebug(
				ctx,
				"task_plugin subsystem=sync event=plugin plugin=%q version=%q action=reuse",
				plugin.Key,
				plugin.Version,
			)
			continue
		}
		logger.LogDebug(
			ctx,
			"task_plugin subsystem=sync event=plugin plugin=%q version=%q action=compile_start",
			plugin.Key,
			plugin.Version,
		)
		var compiled *jsplugin.LoadedPlugin
		source, compileErr := model.GetTaskPluginSource(plugin.Id)
		if compileErr != nil {
			compileErr = fmt.Errorf("load source: %w", compileErr)
		} else {
			compiled, compileErr = jsplugin.CompilePlugin(string(source), jsplugin.Options{Key: plugin.Key, Version: plugin.Version})
		}
		if compileErr != nil {
			retainedIncumbent := false
			if current := currentOverrides[plugin.Key]; current != nil {
				nextOverrides = append(nextOverrides, current)
				retainedIncumbent = true
				if currentHash := taskPluginSyncState.hashes[plugin.Key]; currentHash != "" {
					nextHashes[plugin.Key] = currentHash
				}
			}
			taskPluginSyncState.errors[plugin.Key] = compileErr.Error()
			common.SysError(common.LogText("compile task plugin %s@%s: %v", plugin.Key, plugin.Version, compileErr))
			logger.LogDebug(
				ctx,
				"task_plugin subsystem=sync event=plugin plugin=%q version=%q action=compile_failed retained_incumbent=%t",
				plugin.Key,
				plugin.Version,
				retainedIncumbent,
			)
			continue
		}
		nextOverrides = append(nextOverrides, compiled)
		nextHashes[plugin.Key] = plugin.SourceHash
		delete(taskPluginSyncState.errors, plugin.Key)
		logger.LogDebug(
			ctx,
			"task_plugin subsystem=sync event=plugin plugin=%q version=%q action=compile_success",
			plugin.Key,
			plugin.Version,
		)
	}
	if err = jsplugin.DefaultRegistry.ReplaceOverrides(nextOverrides); err != nil {
		syncErr := fmt.Errorf("publish task plugin generation: %w", err)
		taskPluginSyncState.lastRebuild = taskPluginRebuildOutcome{
			Status:           "failed",
			AttemptedAt:      time.Now(),
			Generation:       jsplugin.DefaultRegistry.Generation().Number,
			DatabaseRevision: databaseSnapshot.Revision,
			Error:            syncErr.Error(),
		}
		logger.LogDebug(
			ctx,
			"task_plugin subsystem=sync event=failed stage=publish retained_generation=%d retained_generation_active=true database_revision=%q elapsed_ms=%d",
			jsplugin.DefaultRegistry.Generation().Number,
			databaseSnapshot.Revision,
			time.Since(started).Milliseconds(),
		)
		return syncErr
	}
	taskPluginSyncState.hashes = nextHashes
	for key := range taskPluginSyncState.errors {
		if !seen[key] {
			delete(taskPluginSyncState.errors, key)
		}
	}
	pluginErrors := jsplugin.DefaultRegistry.RoutingErrors()
	maps.Copy(pluginErrors, taskPluginSyncState.errors)
	pluginErrorCount := len(pluginErrors)
	status := "success"
	if pluginErrorCount > 0 {
		status = "partial"
	}
	taskPluginSyncState.lastRebuild = taskPluginRebuildOutcome{
		Status:           status,
		AttemptedAt:      time.Now(),
		Generation:       jsplugin.DefaultRegistry.Generation().Number,
		DatabaseRevision: databaseSnapshot.Revision,
		PluginErrorCount: pluginErrorCount,
	}
	logger.LogDebug(
		ctx,
		"task_plugin subsystem=sync event=complete database_revision=%q previous_generation=%d generation=%d status=%q active_overrides=%d plugin_errors=%d elapsed_ms=%d",
		databaseSnapshot.Revision,
		generationBefore,
		jsplugin.DefaultRegistry.Generation().Number,
		status,
		len(jsplugin.DefaultRegistry.ActiveOverridePlugins()),
		pluginErrorCount,
		time.Since(started).Milliseconds(),
	)
	return nil
}

func SyncTaskPluginsOnce() {
	if err := syncTaskPluginsOnce(); err != nil {
		common.SysError(err.Error())
	}
}

func SyncTaskPlugins() {
	SyncTaskPluginsOnce()
	for range time.NewTicker(30 * time.Second).C {
		SyncTaskPluginsOnce()
	}
}

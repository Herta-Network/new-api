package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type taskArtifactResponse struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	MimeType   string `json:"mime_type,omitempty"`
	ContentURL string `json:"content_url"`
}

var (
	taskArtifactKeyPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
	errTaskArtifactPluginUnavailable = errors.New("task artifact plugin unavailable")
	errTaskArtifactPlugin            = errors.New("task artifact plugin error")
)

// taskStatusResponse is the status of a task submitted through a task plugin.
type taskStatusResponse struct {
	TaskID     string                `json:"task_id"`
	Platform   constant.TaskPlatform `json:"platform"`
	Status     model.TaskStatus      `json:"status"`
	Progress   string                `json:"progress"`
	FailReason string                `json:"fail_reason"`
	CreatedAt  int64                 `json:"created_at"`
	FinishedAt int64                 `json:"finished_at"`
}

// @Summary	查询任务状态
// @Tags		异步任务
// @Param		key	path		string	true	"任务 ID"
// @Success	200	{object}	taskStatusResponse
// @Security	TokenAuth
// @Router		/v1/tasks/{key} [get]
func GetTask(c *gin.Context) {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), c.Param("key"))
	if err != nil {
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}
	createdAt := task.CreatedAt
	if createdAt == 0 {
		createdAt = task.SubmitTime
	}
	failReason := task.FailReason
	if task.Status == model.TaskStatusSuccess && taskFailReasonIsLegacyResultURL(task.FailReason) {
		failReason = ""
	}
	c.JSON(http.StatusOK, taskStatusResponse{
		TaskID:     task.TaskID,
		Platform:   task.Platform,
		Status:     task.Status,
		Progress:   task.Progress,
		FailReason: failReason,
		CreatedAt:  createdAt,
		FinishedAt: task.FinishTime,
	})
}

// @Summary		列出任务产物
// @Description	任务成功后才有产物。content_url 带签名的 access 参数，可直接下载，不需要 API 密钥。
// @Tags			异步任务
// @Param			key	path		string	true	"任务 ID"
// @Success		200	{object}	taskArtifactsResponse
// @Security		TokenAuth
// @Router			/v1/tasks/{key}/artifacts [get]
func GetTaskArtifacts(c *gin.Context) {
	task, exists, err := model.GetByTaskId(c.GetInt("id"), c.Param("key"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	writeTaskArtifacts(c, task, false)
}

// @Summary		列出任务产物
// @Description	管理员可查看任意任务，其他用户只能查看自己的任务。失败时返回非 200 状态码，code 如 artifact_not_found。
// @Tags			任务
// @Param			task_id	path		string	true	"任务 ID"
// @Success		200		{object}	common.ApiResponse{data=taskArtifactsResponse}
// @Failure		404		{object}	common.ApiResponse
// @Security		UserAuth
// @Router			/api/task/{task_id}/artifacts [get]
func GetDashboardTaskArtifacts(c *gin.Context) {
	task, exists, err := getTaskForArtifactRequest(c, c.Param("task_id"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	writeTaskArtifacts(c, task, true)
}

// taskArtifactsResponse lists a task's artifacts with their content URLs.
type taskArtifactsResponse struct {
	TaskID    string                 `json:"task_id"`
	Artifacts []taskArtifactResponse `json:"artifacts"`
	// LegacyAudioClips are the audio clips of a successful pre-plugin Suno
	// task, listed for the web console only.
	LegacyAudioClips *[]map[string]any `json:"legacy_audio_clips,omitempty"`
	// LegacyContentURL is the video of a successful pre-plugin video task.
	LegacyContentURL string `json:"legacy_content_url,omitempty"`
}

func writeTaskArtifacts(c *gin.Context, task *model.Task, dashboard bool) {
	c.Header("Cache-Control", "private, no-store")
	artifacts, err := projectTaskArtifacts(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	items := make([]taskArtifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		contentURL, buildErr := service.BuildTaskArtifactContentURL(task.TaskID, artifact.Key)
		if buildErr != nil {
			writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_url_error", "Failed to build artifact content URL")
			return
		}
		items = append(items, taskArtifactResponse{
			Key:        artifact.Key,
			Type:       artifact.Type,
			MimeType:   artifact.MimeType,
			ContentURL: contentURL,
		})
	}
	response := taskArtifactsResponse{TaskID: task.TaskID, Artifacts: items}
	if dashboard && task.Status == model.TaskStatusSuccess && task.Platform == constant.TaskPlatformSuno {
		clips := legacySunoAudioClips(task.Data)
		response.LegacyAudioClips = &clips
	}
	if legacyVideoAvailable(task) {
		legacyContentURL, buildErr := service.BuildTaskArtifactContentURL(task.TaskID, "video")
		if buildErr != nil {
			writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_url_error", "Failed to build artifact content URL")
			return
		}
		response.LegacyContentURL = legacyContentURL
	}
	if dashboard {
		common.ApiSuccess(c, response)
		return
	}
	c.JSON(http.StatusOK, response)
}

// legacySunoAudioClips projects a pre-plugin Suno task's persisted snapshot
// into the clips the dashboard audio preview renders. Task lists no longer
// carry the snapshot, so the dashboard reads it here on demand. The snapshot
// is either a clip array or a JSON string holding one; only clips with an
// audio URL are kept and only preview fields are exposed.
func legacySunoAudioClips(data json.RawMessage) []map[string]any {
	clips := make([]map[string]any, 0)
	if len(data) == 0 {
		return clips
	}
	var items []map[string]any
	if err := common.Unmarshal(data, &items); err != nil {
		var encoded string
		if common.Unmarshal(data, &encoded) != nil || common.UnmarshalJsonStr(encoded, &items) != nil {
			return clips
		}
	}
	for _, item := range items {
		audioURL, _ := item["audio_url"].(string)
		if strings.TrimSpace(audioURL) == "" {
			continue
		}
		clip := map[string]any{"audio_url": audioURL}
		for _, key := range []string{"clip_id", "id", "title", "tags", "duration", "image_url", "image_large_url", "metadata"} {
			if value, ok := item[key]; ok {
				clip[key] = value
			}
		}
		clips = append(clips, clip)
	}
	return clips
}

func projectTaskArtifacts(task *model.Task) ([]relaychannel.TaskArtifact, error) {
	if task == nil || task.Status != model.TaskStatusSuccess || !taskHasPluginExecution(task) || !task.ResultRetrievable() {
		return []relaychannel.TaskArtifact{}, nil
	}
	adaptor := relay.GetTaskAdaptor(task.Platform)
	if adaptor == nil {
		return nil, errTaskArtifactPluginUnavailable
	}
	provider, ok := adaptor.(relaychannel.TaskArtifactProvider)
	if !ok {
		return []relaychannel.TaskArtifact{}, nil
	}
	artifacts, err := provider.ListArtifacts(task)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTaskArtifactPlugin, err)
	}
	return validateProjectedTaskArtifacts(artifacts)
}

func validateProjectedTaskArtifacts(artifacts []relaychannel.TaskArtifact) ([]relaychannel.TaskArtifact, error) {
	if len(artifacts) > 64 {
		return nil, fmt.Errorf("%w: too many artifacts", errTaskArtifactPlugin)
	}
	seen := make(map[string]struct{}, len(artifacts))
	for i := range artifacts {
		if artifacts[i].Key != strings.TrimSpace(artifacts[i].Key) ||
			artifacts[i].Type != strings.TrimSpace(artifacts[i].Type) {
			return nil, fmt.Errorf("%w: invalid artifact identity", errTaskArtifactPlugin)
		}
		if !taskArtifactKeyPattern.MatchString(artifacts[i].Key) {
			return nil, fmt.Errorf("%w: invalid artifact key", errTaskArtifactPlugin)
		}
		if _, exists := seen[artifacts[i].Key]; exists {
			return nil, fmt.Errorf("%w: duplicate artifact key", errTaskArtifactPlugin)
		}
		seen[artifacts[i].Key] = struct{}{}
		switch artifacts[i].Type {
		case "video", "audio", "image", "file":
		default:
			return nil, fmt.Errorf("%w: invalid artifact type", errTaskArtifactPlugin)
		}
		if len(artifacts[i].MimeType) > 255 || strings.ContainsAny(artifacts[i].MimeType, "\r\n") {
			return nil, fmt.Errorf("%w: invalid artifact mime type", errTaskArtifactPlugin)
		}
	}
	return artifacts, nil
}

func initTaskArtifactAdaptor(task *model.Task) (relaychannel.TaskAdaptor, error) {
	if task == nil || !taskHasPluginExecution(task) {
		return nil, errTaskArtifactPluginUnavailable
	}
	channelModel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		return nil, fmt.Errorf("%w: channel unavailable", errTaskArtifactPluginUnavailable)
	}
	adaptor := relay.GetTaskAdaptor(task.Platform)
	if adaptor == nil {
		return nil, errTaskArtifactPluginUnavailable
	}
	pluginKey := task.PrivateData.Key
	if pluginKey == "" {
		pluginKey = channelModel.Key
	}
	baseURL := channelModel.GetBaseURL()
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(channelModel.Type)
	}
	adaptor.Init(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:     channelModel.Type,
			ChannelBaseUrl:  baseURL,
			ApiKey:          pluginKey,
			ChannelSetting:  channelModel.GetSetting(),
			HeadersOverride: channelModel.GetHeaderOverride(),
		},
	})
	return adaptor, nil
}

func taskHasPluginExecution(task *model.Task) bool {
	return task != nil &&
		task.PrivateData.Execution != nil &&
		task.PrivateData.Execution.TaskPlugin != nil &&
		strings.TrimSpace(task.PrivateData.Execution.TaskPlugin.Key) != ""
}

func legacyVideoAvailable(task *model.Task) bool {
	if task == nil || task.Status != model.TaskStatusSuccess ||
		taskHasPluginExecution(task) || task.Platform == constant.TaskPlatformSuno ||
		strings.TrimSpace(task.GetResultURL()) == "" {
		return false
	}
	switch constant.NormalizeTaskAction(task.Action) {
	case constant.TaskActionImageToVideo,
		constant.TaskActionTextToVideo,
		constant.TaskActionFirstTailToVideo,
		constant.TaskActionReferenceToVideo,
		constant.TaskActionRemix:
		return true
	default:
		return false
	}
}

func getTaskForArtifactRequest(c *gin.Context, taskID string) (*model.Task, bool, error) {
	if middleware.IsTaskArtifactAccess(c) {
		task, exists, err := model.GetUniqueByOnlyTaskId(taskID)
		if err != nil || !exists || task == nil {
			return task, exists, err
		}
		owner, err := model.GetUserCache(task.UserId)
		if err != nil || owner == nil || owner.Status != common.UserStatusEnabled {
			return nil, false, err
		}
		return task, true, nil
	}
	if c.GetInt("token_id") == 0 && c.GetInt("role") >= common.RoleAdminUser {
		return model.GetByOnlyTaskId(taskID)
	}
	return model.GetByTaskId(c.GetInt("id"), taskID)
}

func writeTaskArtifactProjectionError(c *gin.Context, err error) {
	if errors.Is(err, errTaskArtifactPluginUnavailable) {
		writeTaskArtifactError(c, http.StatusServiceUnavailable, "artifact_plugin_unavailable", "Artifact preview plugin is unavailable")
		return
	}
	writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_plugin_error", "Artifact preview plugin failed")
}

func writeTaskArtifactError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "private, no-store")
	if middleware.IsTaskArtifactAccess(c) {
		status = http.StatusNotFound
		code = "artifact_not_found"
		message = "Task or artifact not found"
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.JSON(status, gin.H{"success": false, "code": code, "message": message})
		return
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    code,
			"code":    code,
		},
	})
}

// @Summary		下载任务产物
// @Description	用 API 密钥访问时只能下载自己的任务产物。也可以不带 API 密钥，改用产物列表 content_url 里的 access 签名参数访问：签名绑定任务和产物，有并发和错误次数限制，签名无效时返回 404。支持 Range 续传和 HEAD。
// @Tags			异步任务
// @Produce		octet-stream
// @Param			key				path		string	true	"任务 ID"
// @Param			artifact_key	path		string	true	"产物 key"
// @Param			access			query		string	false	"签名访问凭证，产物列表的 content_url 自带"
// @Param			Range			header		string	false	"字节范围；If-Range、If-None-Match、If-Modified-Since 也会转发给上游"
// @Success		200				{file}		file	"产物内容"
// @Security		TokenAuth
// @Router			/v1/tasks/{key}/artifacts/{artifact_key}/content [get]
// @Router			/v1/tasks/{key}/artifacts/{artifact_key}/content [head]
func TaskArtifactContent(c *gin.Context) {
	task, exists, err := getTaskForArtifactRequest(c, c.Param("key"))
	if err != nil {
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_internal_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	artifactKey := strings.TrimSpace(c.Param("artifact_key"))
	if !taskArtifactKeyPattern.MatchString(artifactKey) || !task.ResultRetrievable() {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		writeTaskArtifactError(c, http.StatusConflict, "artifact_not_ready", "Task artifacts are not ready")
		return
	}
	if !taskHasPluginExecution(task) {
		if artifactKey != "video" || !legacyVideoAvailable(task) {
			writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
			return
		}
		descriptor := &relaychannel.TaskContentRequest{
			URL:            task.GetResultURL(),
			Method:         c.Request.Method,
			Credentialless: true,
		}
		if err := proxyTaskMedia(c, task, descriptor); err != nil {
			writeTaskMediaProxyError(c, err)
		}
		return
	}
	artifacts, err := projectTaskArtifacts(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	found := false
	for _, artifact := range artifacts {
		if artifact.Key == artifactKey {
			found = true
			break
		}
	}
	if !found {
		writeTaskArtifactError(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	artifactStore := service.GetTaskArtifactStore()
	if ref, resolveErr := artifactStore.Resolve(task, artifactKey); resolveErr == nil && ref != nil {
		_ = artifactStore.Serve(c, task, ref)
		return
	}

	adaptor, err := initTaskArtifactAdaptor(task)
	if err != nil {
		writeTaskArtifactProjectionError(c, err)
		return
	}
	provider, ok := adaptor.(relaychannel.TaskContentRequestProvider)
	if !ok {
		writeTaskArtifactError(c, http.StatusServiceUnavailable, "artifact_plugin_unavailable", "Artifact content plugin is unavailable")
		return
	}
	clientRequest := relaychannel.TaskArtifactClientRequest{
		Method:  c.Request.Method,
		Headers: taskArtifactClientHeaders(c.Request.Header),
	}
	descriptor, err := provider.BuildContentRequest(c.Request.Context(), task, artifactKey, clientRequest)
	if err != nil || descriptor == nil {
		if err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Failed to resolve plugin %s content for task %s: %v", artifactKey, task.TaskID, err))
		}
		writeTaskArtifactError(c, http.StatusInternalServerError, "artifact_plugin_error", "Artifact content plugin failed")
		return
	}
	if err := proxyTaskMedia(c, task, descriptor); err != nil {
		writeTaskMediaProxyError(c, err)
	}
}

func taskArtifactClientHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, 4)
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			result[name] = value
		}
	}
	return result
}

/*
	The task list handlers below deliberately do not call projectTaskArtifacts.
	Artifact projection is confined to the explicit endpoints above.
*/

// @Summary	列出全部任务
// @Tags		任务
// @Param		p				query		int		false	"页码，从 1 开始"
// @Param		page_size		query		int		false	"每页条数"
// @Param		platform		query		string	false	"平台"
// @Param		task_id			query		string	false	"任务 ID"
// @Param		status			query		string	false	"状态"
// @Param		action			query		string	false	"任务类型"
// @Param		start_timestamp	query		int		false	"提交时间下限，Unix 秒"
// @Param		end_timestamp	query		int		false	"提交时间上限，Unix 秒"
// @Param		channel_id		query		string	false	"渠道 ID"
// @Success	200				{object}	common.ApiResponse{data=common.PageInfo{items=[]dto.TaskDto}}
// @Security	AdminAuth
// @Router		/api/task [get]
func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	queryParams := model.SyncTaskQueryParams{Platform: constant.TaskPlatform(c.Query("platform")), TaskID: c.Query("task_id"), Status: c.Query("status"), Action: c.Query("action"), StartTimestamp: startTimestamp, EndTimestamp: endTimestamp, ChannelID: c.Query("channel_id")}
	items := model.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	pageInfo.SetTotal(int(model.TaskCountAllTasks(queryParams)))
	pageInfo.SetItems(tasksToDto(items, true, c.GetInt("role")))
	common.ApiSuccess(c, pageInfo)
}

// @Summary	列出我的任务
// @Tags		任务
// @Param		p				query		int		false	"页码，从 1 开始"
// @Param		page_size		query		int		false	"每页条数"
// @Param		platform		query		string	false	"平台"
// @Param		task_id			query		string	false	"任务 ID"
// @Param		status			query		string	false	"状态"
// @Param		action			query		string	false	"任务类型"
// @Param		start_timestamp	query		int		false	"提交时间下限，Unix 秒"
// @Param		end_timestamp	query		int		false	"提交时间上限，Unix 秒"
// @Success	200				{object}	common.ApiResponse{data=common.PageInfo{items=[]dto.TaskDto}}
// @Security	UserAuth
// @Router		/api/task/self [get]
func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userID := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	queryParams := model.SyncTaskQueryParams{Platform: constant.TaskPlatform(c.Query("platform")), TaskID: c.Query("task_id"), Status: c.Query("status"), Action: c.Query("action"), StartTimestamp: startTimestamp, EndTimestamp: endTimestamp}
	items := model.TaskGetAllUserTask(userID, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	pageInfo.SetTotal(int(model.TaskCountAllUserTask(userID, queryParams)))
	pageInfo.SetItems(tasksToDto(items, false, common.RoleCommonUser))
	common.ApiSuccess(c, pageInfo)
}

func tasksToDto(tasks []*model.Task, fillUser bool, viewerRole int) []*dto.TaskDto {
	var userIDMap map[int]*model.UserBase
	if fillUser {
		userIDMap = make(map[int]*model.UserBase)
		userIDs := types.NewSet[int]()
		for _, task := range tasks {
			userIDs.Add(task.UserId)
		}
		for _, userID := range userIDs.Items() {
			if cacheUser, err := model.GetUserCache(userID); err == nil {
				userIDMap[userID] = cacheUser
			}
		}
	}
	result := make([]*dto.TaskDto, len(tasks))
	for i, task := range tasks {
		if fillUser {
			if user, ok := userIDMap[task.UserId]; ok {
				task.Username = user.Username
			}
		}
		item := relay.TaskModel2Dto(task)
		item.LegacyVideoAvailable = legacyVideoAvailable(task)
		item.ResultDiscarded = task.PrivateData.ResultDiscarded
		if task.Status == model.TaskStatusSuccess {
			item.ResultURL = ""
			if taskFailReasonIsLegacyResultURL(task.FailReason) {
				item.FailReason = ""
			}
		}
		if viewerRole >= common.RoleAdminUser {
			adminInfo := &dto.TaskAdminInfo{}
			if execution := task.PrivateData.Execution; execution != nil {
				adminInfo.RequestID = execution.RequestID
				adminInfo.RequestPath = execution.RequestPath
				if snapshot := execution.TaskPlugin; snapshot != nil {
					adminInfo.TaskPlugin = &dto.TaskPluginInfo{
						Key:     snapshot.Key,
						Name:    snapshot.Name,
						Version: snapshot.Version,
					}
					if snapshot.Author != nil {
						adminInfo.TaskPlugin.Author = &dto.TaskPluginAuthorInfo{
							Name: snapshot.Author.Name,
							URL:  snapshot.Author.URL,
						}
					}
				}
			}
			if adminInfo.RequestID != "" || adminInfo.RequestPath != "" || adminInfo.TaskPlugin != nil {
				item.AdminInfo = adminInfo
			}
		}
		if viewerRole >= common.RoleRootUser {
			rootInfo := &dto.TaskRootInfo{
				UpstreamTaskID: task.PrivateData.UpstreamTaskID,
				NodeName:       task.PrivateData.NodeName,
			}
			if execution := task.PrivateData.Execution; execution != nil {
				if snapshot := execution.TaskPlugin; snapshot != nil {
					rootInfo.TaskPlugin = &dto.TaskPluginRuntimeInfo{
						Key:        snapshot.Key,
						Version:    snapshot.Version,
						APIVersion: snapshot.APIVersion,
						Generation: snapshot.Generation,
					}
				}
			}
			if rootInfo.TaskPlugin != nil || rootInfo.UpstreamTaskID != "" || rootInfo.NodeName != "" {
				item.RootInfo = rootInfo
			}
		}
		result[i] = item
	}
	return result
}

func taskFailReasonIsLegacyResultURL(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= len("https://") && strings.EqualFold(value[:len("https://")], "https://") ||
		len(value) >= len("http://") && strings.EqualFold(value[:len("http://")], "http://") ||
		len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:")
}

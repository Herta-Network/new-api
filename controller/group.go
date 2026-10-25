package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// @Summary	列出分组
// @Tags		分组
// @Success	200	{object}	common.ApiResponse{data=[]string}
// @Security	AdminAuth
// @Router		/api/group/ [get]
func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	common.ApiSuccess(c, groupNames)
}

// GetGroupUsage reports how many users and tokens use each group.
//
//	@Summary	统计分组使用量
//	@Tags		分组
//	@Success	200	{object}	common.ApiResponse{data=model.GroupUsage}
//	@Security	AdminAuth
//	@Router		/api/group/usage [get]
func GetGroupUsage(c *gin.Context) {
	usage, err := model.GetGroupUsage()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, usage)
}

type usableGroup struct {
	// Ratio is the group ratio for the user's group, or "auto" for the auto
	// group.
	Ratio any    `json:"ratio"`
	Desc  string `json:"desc"`
}

//	@Summary		获取可用分组
//	@Description	无需登录，返回站点默认的可用分组，不含按用户分组设置的增减。
//	@Tags			分组
//	@Success		200	{object}	common.ApiResponse{data=map[string]usableGroup}	"键为分组名"
//	@Router			/api/user/groups [get]

// @Summary	获取我的可用分组
// @Tags		个人
// @Success	200	{object}	common.ApiResponse{data=map[string]usableGroup}	"键为分组名"
// @Security	UserAuth
// @Router		/api/user/self/groups [get]
func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]usableGroup)
	userGroup := ""
	userId := c.GetInt("id")
	userGroup, _ = model.GetUserGroup(userId, false)
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = usableGroup{
				Ratio: service.GetUserGroupRatio(userGroup, groupName),
				Desc:  desc,
			}
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		usableGroups["auto"] = usableGroup{
			Ratio: "auto",
			Desc:  setting.GetUsableGroupDescription("auto"),
		}
	}
	common.ApiSuccess(c, usableGroups)
}

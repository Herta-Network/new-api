package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// missingModelsResponse is GetMissingModels' body, which has no message.
type missingModelsResponse struct {
	Success bool     `json:"success"`
	Data    []string `json:"data"`
}

// GetMissingModels returns the list of model names that are referenced by channels
// but do not have corresponding records in the models meta table.
// This helps administrators quickly discover models that need configuration.
//
//	@Summary		列出缺少元数据的模型
//	@Description	响应体没有 message 字段。
//	@Tags			模型
//	@Success		200	{object}	missingModelsResponse
//	@Security		AdminAuth
//	@Router			/api/models/missing [get]
func GetMissingModels(c *gin.Context) {
	missing, err := model.GetMissingModels()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, missingModelsResponse{Success: true, Data: missing})
}

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	pluginContractVersionHeader  = "x-kerkerker-contract-version"
	pluginContractVersionsHeader = "x-kerkerker-contract-versions"
	pluginRequestIDHeader        = "x-kerkerker-request-id"
)

type PluginHandler struct {
	tmdb        *service.TMDBService
	imageSyncer *service.ImageSyncer
}

func NewPluginHandler(tmdb *service.TMDBService, imageSyncers ...*service.ImageSyncer) *PluginHandler {
	var imageSyncer *service.ImageSyncer
	if len(imageSyncers) > 0 {
		imageSyncer = imageSyncers[0]
	}
	return &PluginHandler{tmdb: tmdb, imageSyncer: imageSyncer}
}

func (h *PluginHandler) setContractHeaders(c *gin.Context) {
	c.Header(pluginContractVersionHeader, service.PluginContractV1)
	c.Header("cache-control", "no-store")
}

func (h *PluginHandler) Manifest(c *gin.Context) {
	h.setContractHeaders(c)
	c.JSON(http.StatusOK, gin.H{
		"id":              service.TMDBPluginID,
		"name":            "Kerkerker TMDB Content",
		"version":         service.TMDBPluginVersion,
		"contractVersion": service.PluginContractV1,
		"capabilities": []gin.H{
			{"id": "content.catalog", "version": service.PluginContractV1},
			{"id": "content.calendar", "version": service.PluginContractV1},
			{"id": "content.detail", "version": service.PluginContractV1},
			{"id": "content.search", "version": service.PluginContractV1},
			{"id": "asset.image", "version": service.PluginContractV1},
		},
		"locales": []string{"en-US"},
		"config": gin.H{
			"version": "1.0",
			"fields": []gin.H{
				{"key": "serviceUrl", "type": "url", "required": true},
				{"key": "serviceToken", "type": "secret", "required": true, "secret": true},
			},
		},
		"permissions": gin.H{
			"networkHosts": []string{},
			"secrets":      []string{"serviceToken"},
			"storage":      "none",
		},
		"compliance": gin.H{
			"legalBasis":         "operator-approved-tmdb-api-terms",
			"termsUrl":           "https://www.themoviedb.org/terms-of-use",
			"contentScope":       []string{"movie-and-series-metadata", "poster-and-backdrop-image-references"},
			"regions":            []string{"GLOBAL"},
			"dataClassification": "licensed",
		},
	})
}

func (h *PluginHandler) Health(c *gin.Context) {
	h.setContractHeaders(c)
	if !negotiateContract(c) {
		return
	}
	if h.tmdb == nil || !h.tmdb.IsConfigured() {
		writePluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "TMDB 插件上游未配置", Status: http.StatusServiceUnavailable})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":          "ready",
		"pluginId":        service.TMDBPluginID,
		"version":         service.TMDBPluginVersion,
		"contractVersion": service.PluginContractV1,
		"dependencies":    gin.H{"tmdb": gin.H{"configured": true}},
	})
}

type pluginInvokeEnvelope struct {
	ContractVersion string                `json:"contractVersion"`
	Capability      string                `json:"capability"`
	Operation       string                `json:"operation"`
	Context         service.PluginContext `json:"context"`
	Request         json.RawMessage       `json:"request"`
}

func (h *PluginHandler) Invoke(c *gin.Context) {
	h.setContractHeaders(c)
	if !negotiateContract(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	var envelope pluginInvokeEnvelope
	if err := c.ShouldBindJSON(&envelope); err != nil {
		h.pluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件调用请求格式无效", Status: http.StatusBadRequest})
		return
	}
	if envelope.ContractVersion == "" || !compatibleContract(envelope.ContractVersion) || (envelope.Context.ContractVersion != "" && !compatibleContract(envelope.Context.ContractVersion)) {
		h.pluginError(c, &service.PluginFault{Code: "CONTRACT_VERSION_UNSUPPORTED", Message: "插件契约版本不兼容", Status: http.StatusConflict})
		return
	}
	if envelope.Context.Locale == "" {
		h.pluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件调用缺少 locale", Status: http.StatusBadRequest})
		return
	}
	if envelope.Context.Runtime != "" && envelope.Context.Runtime != "server" {
		h.pluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件调用 runtime 无效", Status: http.StatusBadRequest})
		return
	}
	if envelope.Context.RequestID == "" {
		h.pluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件调用缺少 requestId", Status: http.StatusBadRequest})
		return
	}
	if headerRequestID := strings.TrimSpace(c.GetHeader(pluginRequestIDHeader)); headerRequestID != "" && headerRequestID != envelope.Context.RequestID {
		h.pluginError(c, &service.PluginFault{Code: "INVALID_REQUEST", Message: "请求 ID 不一致", Status: http.StatusBadRequest})
		return
	}
	if !strings.EqualFold(envelope.Context.Profile, "en-default") {
		h.pluginError(c, &service.PluginFault{Code: "CONFIGURATION_ERROR", Message: "TMDB 插件只允许 en-default 画像", Status: http.StatusBadRequest})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, fault := h.tmdb.InvokePlugin(ctx, envelope.Capability, envelope.Operation, envelope.Request, envelope.Context)
	if fault != nil {
		h.pluginError(c, fault)
		return
	}
	result = preparePluginImages(result, h.imageSyncer)
	c.JSON(http.StatusOK, result)
}

func compatibleContract(value string) bool {
	return value == service.PluginContractV1 || value == "1.0"
}

func negotiateContract(c *gin.Context) bool {
	requested := strings.TrimSpace(c.GetHeader(pluginContractVersionsHeader))
	if requested == "" {
		requested = strings.TrimSpace(c.GetHeader(pluginContractVersionHeader))
	}
	if requested == "" {
		return true
	}
	for _, version := range strings.Split(requested, ",") {
		if compatibleContract(strings.TrimSpace(version)) {
			return true
		}
	}
	h := &service.PluginFault{Code: "CONTRACT_VERSION_UNSUPPORTED", Message: "插件服务不支持请求的契约版本", Status: http.StatusConflict}
	writePluginError(c, h)
	return false
}

func (h *PluginHandler) pluginError(c *gin.Context, fault *service.PluginFault) {
	writePluginError(c, fault)
}

func writePluginError(c *gin.Context, fault *service.PluginFault) {
	if fault == nil {
		fault = &service.PluginFault{Code: "UPSTREAM_ERROR", Message: "插件调用失败", Retryable: true, Status: http.StatusBadGateway}
	}
	status := fault.Status
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	body := gin.H{
		"code":      fault.Code,
		"message":   fault.Message,
		"retryable": fault.Retryable,
	}
	if fault.Details != nil {
		body["details"] = fault.Details
	}
	if requestID := strings.TrimSpace(c.GetHeader(pluginRequestIDHeader)); requestID != "" && len(requestID) <= 240 {
		body["requestId"] = requestID
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}

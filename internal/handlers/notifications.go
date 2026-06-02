package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/signals"
)

func (a *API) listNotifications(c *gin.Context) {
	if a.notify == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "notifications not configured"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := a.notify.List(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (a *API) markNotificationRead(c *gin.Context) {
	if a.notify == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "notifications not configured"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := a.notify.MarkRead(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type emitBody struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// emitNotification triggers a signal for demos (requires JWT + procurement.emit_notification).
// Use ?broadcast=1 to publish only to Redis (subscriber inserts a feed row; no local bus handlers).
func (a *API) emitNotification(c *gin.Context) {
	var body emitBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Event == events.ProcurementAlert || body.Event == events.RequisitionPending {
		if err := notifications.ValidateEmitPayload(body.Event, body.Payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else if len(body.Payload) == 0 {
		body.Payload = []byte("{}")
	}
	ev := signals.Event{Name: body.Event, Payload: body.Payload}

	if c.DefaultQuery("broadcast", "") == "1" {
		if a.cache == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "redis cache not configured"})
			return
		}
		if err := signals.Broadcast(c.Request.Context(), a.cache.Redis(), a.cfg.SignalRedisChannel, ev); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "mode": "broadcast"})
		return
	}

	if a.bus == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "signal bus not configured"})
		return
	}

	switch body.Event {
	case events.ProcurementAlert, events.RequisitionPending:
		if err := a.bus.Emit(c.Request.Context(), ev); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "mode": "emit"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown event", "allowed": []string{events.ProcurementAlert, events.RequisitionPending}})
	}
}

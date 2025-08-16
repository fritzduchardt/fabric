package restapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// TelegramHandler handles sending messages to Telegram
type TelegramHandler struct {
	botToken string
}

// TelegramMessageRequest represents a request to send a message to Telegram
type TelegramMessageRequest struct {
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
}

// NewTelegramHandler creates a new TelegramHandler and registers its routes
func NewTelegramHandler(r *gin.Engine) *TelegramHandler {
	handler := &TelegramHandler{
		botToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
	}

	r.POST("/telegram/send", handler.SendMessage)
	return handler
}

// SendMessage handles the POST /telegram/send endpoint
func (h *TelegramHandler) SendMessage(c *gin.Context) {
	if h.botToken == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "TELEGRAM_BOT_TOKEN not set"})
		return
	}

	var req TelegramMessageRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request format: %v", err)})
		return
	}

	if req.ChatID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chat_id is required"})
		return
	}

	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// Prepare the request to Telegram API
	telegramReq := map[string]string{
		"chat_id": req.ChatID,
		"text":    req.Message,
	}

	jsonData, err := json.Marshal(telegramReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error marshaling request: %v", err)})
		return
	}

	// Send the request to Telegram API
	telegramURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", h.botToken)
	resp, err := http.Post(telegramURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error sending message to Telegram: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorResponse map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&errorResponse); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error from Telegram API: %s", resp.Status)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error from Telegram API: %v", errorResponse)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Message sent successfully"})
}

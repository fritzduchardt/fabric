package restapi

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/danielmiessler/fabric/common"
	"github.com/danielmiessler/fabric/core"
	"github.com/danielmiessler/fabric/plugins/db/fsdb"
	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	registry *core.PluginRegistry
	db       *fsdb.Db
}

type PromptRequest struct {
	UserInput    string `json:"userInput"`
	Vendor       string `json:"vendor"`
	Model        string `json:"model"`
	ContextName  string `json:"contextName"`
	PatternName  string `json:"patternName"`
	StrategyName string `json:"strategyName"`
	ObsidianFile string `json:"obsidianFile"`
	SessionName  string `json:"sessionName"`
}

type ChatRequest struct {
	Prompts  []PromptRequest `json:"prompts"`
	Language string          `json:"language"`
	common.ChatOptions
}

type StreamResponse struct {
	Type    string `json:"type"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

func NewChatHandler(r *gin.Engine, registry *core.PluginRegistry, db *fsdb.Db) *ChatHandler {
	handler := &ChatHandler{
		registry: registry,
		db:       db,
	}
	r.POST("/chat", handler.HandleChat)
	r.POST("/storelast", handler.StoreLast)
	return handler
}

func (h *ChatHandler) HandleChat(c *gin.Context) {
	var request ChatRequest

	if err := c.BindJSON(&request); err != nil {
		log.Printf("Error binding JSON: %v", err)
		c.Writer.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request format: %v", err)})
		return
	}

	fabricHome := os.Getenv("FABRIC_CONFIG_HOME")
	if fabricHome == "" {
		log.Printf("FABRIC_CONFIG_HOME not set, skipping context file write")
	} else {
		contextDir := filepath.Join(fabricHome, "contexts")
		if err := os.MkdirAll(contextDir, 0755); err != nil {
			log.Printf("Error creating context directory %s: %v", contextDir, err)
		} else {
			now := time.Now()
			filename := filepath.Join(contextDir, "general_context.md")
			content := fmt.Sprintf("# CONTEXT\n - The current date and time is: %s\n", now.Format("2006-01-02 15:04:05"))
			if err := ioutil.WriteFile(filename, []byte(content), 0644); err != nil {
				log.Printf("Error writing context file %s: %v", filename, err)
			} else {
				log.Printf("Wrote context file: %s", filename)
			}
		}
	}

	log.Printf("Received chat request - Language: '%s', Prompts: %d", request.Language, len(request.Prompts))

	c.Writer.Header().Set("Content-Type", "text/readystream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	clientGone := c.Writer.CloseNotify()

	for i, prompt := range request.Prompts {
		select {
		case <-clientGone:
			log.Printf("Client disconnected")
			return
		default:
			log.Printf("Processing prompt %d: Model=%s Pattern=%s Context=%s ObsidianFile=%s",
				i+1, prompt.Model, prompt.PatternName, prompt.ContextName, prompt.ObsidianFile)

			streamChan := make(chan string)
			go func(p PromptRequest) {
				defer close(streamChan)

				if p.StrategyName != "" {
					strategyFile := filepath.Join(os.Getenv("HOME"), ".config", "fabric", "strategies", p.StrategyName+".json")
					data, err := ioutil.ReadFile(strategyFile)
					if err == nil {
						var s struct {
							Prompt string `json:"prompt"`
						}
						if err := json.Unmarshal(data, &s); err == nil && s.Prompt != "" {
							p.UserInput = s.Prompt + "\n" + p.UserInput
						}
					}
				}

				var obsidianFilePath string
				if p.ObsidianFile != "" {
					obsidianVaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
					if obsidianVaultPath == "" {
						obsidianVaultPath = filepath.Join(os.Getenv("HOME"), "Documents/Obsidian")
						log.Printf("Obsidian Vault Path not set. Defaulting to: %s", obsidianVaultPath)
					}
					obsidianFilePath = filepath.Join(obsidianVaultPath, p.ObsidianFile)
					if !strings.HasSuffix(obsidianFilePath, ".md") {
						obsidianFilePath += ".md"
					}
					if _, err := os.Stat(obsidianFilePath); err == nil {
						fileContent, err := ioutil.ReadFile(obsidianFilePath)
						fileContentAmended := "FILENAME: " + string(obsidianFilePath) + "\n" + string(fileContent)
						if err == nil {
							escapedContent := strings.ReplaceAll(fileContentAmended, "\n", "\\n")
							p.UserInput = p.UserInput + ":" + escapedContent
							log.Printf("Added content from obsidian file: %s", obsidianFilePath)
						} else {
							log.Printf("Error reading obsidian file %s: %v", obsidianFilePath, err)
						}
					}
				}

				chatter, err := h.registry.GetChatter(p.Model, 2048, "", false, false)
				if err != nil {
					log.Printf("Error creating chatter: %v", err)
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}

				chatReq := &common.ChatRequest{
					Message: &goopenai.ChatCompletionMessage{
						Role:    "user",
						Content: p.UserInput,
					},
					PatternName: p.PatternName,
					ContextName: p.ContextName,
					Language:    request.Language,
					SessionName: p.SessionName,
				}

				opts := &common.ChatOptions{
					Model:            p.Model,
					Temperature:      request.Temperature,
					TopP:             request.TopP,
					FrequencyPenalty: request.FrequencyPenalty,
					PresencePenalty:  request.PresencePenalty,
				}

				session, err := chatter.Send(chatReq, opts)
				if err != nil {
					log.Printf("Error from chatter.Send: %v", err)
					streamChan <- fmt.Sprintf("Error: %v", err)
					return
				}
				if session == nil {
					log.Printf("No session returned from chatter.Send")
					streamChan <- "Error: No response from model"
					return
				}

				lastMsg := session.GetLastMessage()
				if lastMsg != nil {
					content := lastMsg.Content
					streamChan <- content
					if obsidianFilePath != "" && prompt.PatternName == "obsidian_author" {
						dir := filepath.Dir(obsidianFilePath)
						if err := os.MkdirAll(dir, 0755); err != nil {
							log.Printf("Error creating directory for obsidian file: %v", err)
						} else {
							if err := ioutil.WriteFile(obsidianFilePath, []byte(content), 0644); err != nil {
								log.Printf("Error writing to obsidian file %s: %v", obsidianFilePath, err)
							} else {
								log.Printf("Successfully saved response to %s", obsidianFilePath)
							}
						}
					}
				} else {
					log.Printf("No message content in session")
					streamChan <- "Error: No response content"
				}
			}(prompt)

			for content := range streamChan {
				select {
				case <-clientGone:
					return
				default:
					var response StreamResponse
					if strings.HasPrefix(content, "Error:") {
						response = StreamResponse{"error", "plain", content}
					} else {
						response = StreamResponse{"content", content, content}
					}
					if err := writeSSEResponse(c.Writer, response); err != nil {
						log.Printf("Error writing response: %v", err)
						return
					}
				}
			}

			completeResponse := StreamResponse{"complete", "plain", ""}
			if err := writeSSEResponse(c.Writer, completeResponse); err != nil {
				log.Printf("Error writing completion response: %v", err)
				return
			}
		}
	}
}

func (h *ChatHandler) StoreLast(c *gin.Context) {
	var req struct {
		SessionName string `json:"sessionName"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.SessionName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sessionName is required"})
		return
	}
	fabricHome := os.Getenv("FABRIC_CONFIG_HOME")
	if fabricHome == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FABRIC_CONFIG_HOME not set"})
		return
	}
	sessionsDir := filepath.Join(fabricHome, "sessions")
	data, err := ioutil.ReadFile(filepath.Join(sessionsDir, req.SessionName+".json"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error reading session file: %v", err)})
		return
	}
	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error parsing session JSON: %v", err)})
		return
	}
	var assistantContent string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			assistantContent = msgs[i].Content
			break
		}
	}
	if assistantContent == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no assistant message found in session"})
		return
	}
	var savedFilename string
	lines := strings.Split(assistantContent, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "FILENAME:") {
		savedFilename = strings.TrimSpace(strings.TrimPrefix(lines[0], "FILENAME:"))
		assistantContent = strings.Join(lines[1:], "\n")
		dir := filepath.Dir(savedFilename)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating directory: %v", err)})
				return
			}
		}
		if err := ioutil.WriteFile(savedFilename, []byte(assistantContent), 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing file %s: %v", savedFilename, err)})
			return
		}
		log.Printf("Stored assistant content to %s", savedFilename)
	}
	c.JSON(http.StatusOK, gin.H{
		"message":  "Last content stored successfully",
		"content":  assistantContent,
		"filename": savedFilename,
	})
}

func writeSSEResponse(w gin.ResponseWriter, response StreamResponse) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("error marshaling response: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(data)); err != nil {
		return fmt.Errorf("error writing response: %v", err)
	}
	w.(http.Flusher).Flush()
	return nil
}

func detectFormat(content string) string {
	if strings.HasPrefix(content, "graph TD") ||
		strings.HasPrefix(content, "gantt") ||
		strings.HasPrefix(content, "flowchart") ||
		strings.HasPrefix(content, "sequenceDiagram") ||
		strings.HasPrefix(content, "classDiagram") ||
		strings.HasPrefix(content, "stateDiagram") {
		return "mermaid"
	}
	return "markdown"
}

package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

var modelMapping = map[string]string{
	"gpt-4o-mini":  "GPT-4o mini",
	"claude-haiku": "Claude Haiku",
	"llama-3":      "Llama 3",
	"gemini-1.5":   "Gemini 1.5",
	"gemini-flash": "Gemini Flash",
	"command-r":    "Command R",
}

func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

type OpenAIRequest struct {
	Messages []Message `json:"messages"`
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	Id      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type ChatCompletionStreamResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			Role    string `json:"role,omitempty"`
		} `json:"delta"`
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	authToken := os.Getenv("AUTH_TOKEN")
	if authToken != "" {
		requestToken := r.Header.Get("Authorization")
		if requestToken == "" {
			http.Error(w, "Access Denied", http.StatusUnauthorized)
			return
		}
		requestToken = strings.TrimPrefix(requestToken, "Bearer ")
		if requestToken != authToken {
			http.Error(w, "Access Denied", http.StatusUnauthorized)
			return
		}
	}

	if r.URL.Path != "/v1/chat/completions" {
		w.Header().Set("Content-Type", "application/json")
		response := map[string]string{
			"status":  "Julius2Api Service Running...",
			"message": "MoLoveSze...",
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var openAIReq OpenAIRequest
	if err := json.NewDecoder(r.Body).Decode(&openAIReq); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if mappedModel, exists := modelMapping[openAIReq.Model]; exists {
		openAIReq.Model = mappedModel
	} else {
		openAIReq.Model = "GPT-4o mini"
	}

	tempUserID, err := getTempUserID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	juliusResp, err := sendToJulius(tempUserID, openAIReq.Messages[len(openAIReq.Messages)-1].Content, openAIReq.Model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isStream := openAIReq.Stream

	respId := "chatcmpl-" + tempUserID
	created := getCurrentTimestamp()

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		chunks := splitIntoChunks(juliusResp, 50)
		firstResponse := ChatCompletionStreamResponse{
			ID:      respId,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   openAIReq.Model,
			Choices: []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role,omitempty"`
				} `json:"delta"`
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason,omitempty"`
			}{
				{
					Delta: struct {
						Content string `json:"content"`
						Role    string `json:"role,omitempty"`
					}{
						Role: "assistant",
					},
					Index: 0,
				},
			},
		}

		data, err := json.Marshal(firstResponse)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", string(data))

		for i, chunk := range chunks {
			response := ChatCompletionStreamResponse{
				ID:      respId,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   openAIReq.Model,
				Choices: []struct {
					Delta struct {
						Content string `json:"content"`
						Role    string `json:"role,omitempty"`
					} `json:"delta"`
					Index        int    `json:"index"`
					FinishReason string `json:"finish_reason,omitempty"`
				}{
					{
						Delta: struct {
							Content string `json:"content"`
							Role    string `json:"role,omitempty"`
						}{
							Content: chunk,
						},
						Index: 0,
						FinishReason: func() string {
							if i == len(chunks)-1 {
								return "stop"
							}
							return ""
						}(),
					},
				},
			}

			data, err := json.Marshal(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", string(data))
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
	} else {
		w.Header().Set("Content-Type", "application/json")
		response := OpenAIResponse{
			Id:      respId,
			Object:  "chat.completion",
			Created: created,
			Model:   openAIReq.Model,
			Choices: []Choice{
				{
					Index: 0,
					Message: Message{
						Role:    "assistant",
						Content: juliusResp,
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}
}

func splitIntoChunks(text string, chunkSize int) []string {
	var chunks []string
	runes := []rune(text)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

func getTempUserID() (string, error) {
	resp, err := http.Get("https://playground.julius.ai/api/temp_user_id")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Status     string `json:"status"`
		TempUserID string `json:"temp_user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.TempUserID, nil
	//return "自定义ID", nil
}

func sendToJulius(tempUserID, message string, model string) (string, error) {
	conversationID := uuid.New().String()

	juliusReq := map[string]interface{}{
		"message": map[string]interface{}{
			"content": message,
			"role":    "user",
		},
		"provider":         "default",
		"chat_mode":        "auto",
		"client_version":   "20240130",
		"theme":            "dark",
		"new_images":       nil,
		"new_attachments":  nil,
		"dataframe_format": "json",
		"selectedModels": []string{
			model,
		},
	}

	reqBody, err := json.Marshal(juliusReq)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://playground.julius.ai/api/chat/message", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("is-demo", tempUserID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Platform", "web")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("conversation-id", conversationID)
	req.Header.Set("interactive-charts", "true")
	req.Header.Set("use-dict", "true")
	req.Header.Set("Gcs", "true")
	req.Header.Set("Is-Native", "false")
	req.Header.Set("sec-ch-ua-platform", "Windows")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var fullResponse strings.Builder
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		var jsonResp map[string]interface{}
		if err := json.Unmarshal([]byte(line), &jsonResp); err != nil {
			continue
		}
		if content, ok := jsonResp["content"].(string); ok {
			fullResponse.WriteString(content)
		}
	}
	return fullResponse.String(), nil
}

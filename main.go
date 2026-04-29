package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// --- Cookie Structs ---
type CookieJSON struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// --- Response Structs ---
type VideoFormat struct {
	Quality  string `json:"quality"`
	MimeType string `json:"mimeType"`
	URL      string `json:"url"`
}

type APIResponse struct {
	Status  string        `json:"status"`
	VideoID string        `json:"video_id,omitempty"`
	Formats []VideoFormat `json:"formats,omitempty"`
	Message string        `json:"message,omitempty"`
	RawData string        `json:"raw_data,omitempty"` // ---> NEW: For debugging YouTube's raw response
}

// Global raw cookie string
var rawCookieHeader string

// Load cookies from JSON file at startup and build a raw string
func loadCookies(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Println("⚠️ Cookies file not found:", err)
		return
	}
	defer file.Close()

	var rawCookies []CookieJSON
	err = json.NewDecoder(file).Decode(&rawCookies)
	if err != nil {
		fmt.Println("⚠️ Error decoding cookies:", err)
		return
	}

	var parts []string
	for _, c := range rawCookies {
		// Go ki strictness se bachne ke liye double quotes aur newlines remove kar rahe hain
		cleanValue := strings.ReplaceAll(c.Value, "\"", "")
		cleanValue = strings.ReplaceAll(cleanValue, "\n", "")
		cleanValue = strings.ReplaceAll(cleanValue, "\r", "")
		
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, cleanValue))
	}
	
	// Create a single raw cookie string format: "name1=value1; name2=value2"
	rawCookieHeader = strings.Join(parts, "; ")
	fmt.Printf("🍪 Loaded %d Cookies Successfully (Raw String Mode)!\n", len(rawCookies))
}

// Extract Video ID from YouTube URL
func extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		parts := strings.Split(url, "v=")
		idPart := strings.Split(parts[1], "&")[0]
		return idPart
	} else if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		idPart := strings.Split(parts[1], "?")[0]
		return idPart
	}
	return url // Assume it's already an ID if no match
}

// Main API Handler
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get URL from query parameters (e.g., ?url=https://youtu.be/...)
	videoURL := r.URL.Query().Get("url")
	if videoURL == "" {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "URL parameter is missing"})
		return
	}

	videoID := extractVideoID(videoURL)
	targetURL := "https://m.youtube.com/watch?v=" + videoID

	// Create a fast HTTP client
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Failed to create request"})
		return
	}

	// ---> STRICT ANDROID HEADERS <---
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Ch-Ua-Platform", "\"Android\"")
	req.Header.Set("Sec-Ch-Ua-Mobile", "?1")

	// ---> INJECT RAW COOKIE HEADER DIRECTLY <---
	if rawCookieHeader != "" {
		req.Header.Set("Cookie", rawCookieHeader)
	}

	// Send Request
	resp, err := client.Do(req)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Failed to fetch video page"})
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Failed to read response"})
		return
	}
	bodyString := string(bodyBytes)

	// Regex to find the internal JSON data
	re := regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*({.+?});var`)
	match := re.FindStringSubmatch(bodyString)
	if len(match) < 2 {
		// Agar regex fail ho jaye, to poori HTML bhej do taake pata chale YT ne kya bheja
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Video JSON data not found in HTML. Checking raw_data field for YouTube's HTML response.",
			RawData: bodyString,
		})
		return
	}

	// Parse the massive JSON blob
	var playerData map[string]interface{}
	err = json.Unmarshal([]byte(match[1]), &playerData)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Failed to parse YouTube's JSON data. Check raw_data.",
			RawData: match[1],
		})
		return
	}

	// Check Playability Status
	playability, _ := playerData["playabilityStatus"].(map[string]interface{})
	status, _ := playability["status"].(string)
	if status != "OK" {
		reason, _ := playability["reason"].(string)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Playability Error: " + reason,
			RawData: match[1], // Sending raw JSON to see exact playability restriction
		})
		return
	}

	// Extract Streaming Data
	streamingData, ok := playerData["streamingData"].(map[string]interface{})
	if !ok {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "No streamingData object found. Check raw_data.",
			RawData: match[1], // Yahan se structure dekh sakte ho
		})
		return
	}

	var extractedFormats []VideoFormat

	// Helper function to process formats
	processFormatList := func(formatList interface{}) {
		formats, ok := formatList.([]interface{})
		if !ok {
			return
		}
		for _, f := range formats {
			fmtData, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			
			quality, _ := fmtData["qualityLabel"].(string)
			if quality == "" {
				quality = "Audio Only / Unknown"
			}
			mime, _ := fmtData["mimeType"].(string)
			url, _ := fmtData["url"].(string)

			if url != "" {
				extractedFormats = append(extractedFormats, VideoFormat{
					Quality:  quality,
					MimeType: strings.Split(mime, ";")[0],
					URL:      url,
				})
			}
		}
	}

	// Parse both combined formats and adaptive formats
	processFormatList(streamingData["formats"])
	processFormatList(streamingData["adaptiveFormats"])

	if len(extractedFormats) == 0 {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Could not find direct URLs. Video might be using Cipher protection. Check raw_data.",
			RawData: match[1], // YT JSON for cipher debugging
		})
		return
	}

	// Send Success Response
	json.NewEncoder(w).Encode(APIResponse{
		Status:  "success",
		VideoID: videoID,
		Formats: extractedFormats,
	})
}

func main() {
	loadCookies("youtube_cookies.json")

	http.HandleFunc("/api/download", downloadHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("🚀 Go API Server running on port", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println("Server Error:", err)
	}
}

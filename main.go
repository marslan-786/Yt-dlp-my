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
	RawData string        `json:"raw_data,omitempty"`
}

// Global raw cookie string
var rawCookieHeader string

// Load cookies from JSON file
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
	hasConsent := false

	for _, c := range rawCookies {
		// Newlines remove kar rahe hain magar double quotes (") ko mehfooz rakha hai
		cleanValue := strings.ReplaceAll(c.Value, "\n", "")
		cleanValue = strings.ReplaceAll(cleanValue, "\r", "")
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, cleanValue))
		
		if strings.ToUpper(c.Name) == "CONSENT" {
			hasConsent = true
		}
	}
	
	// CONSENT cookie auto-add taake Youtube terms wala page bypass ho
	if !hasConsent {
		parts = append(parts, "CONSENT=YES+cb.20210328-17-p0.en+FX+478")
	}
	
	rawCookieHeader = strings.Join(parts, "; ")
	fmt.Printf("🍪 Loaded %d Cookies Successfully!\n", len(rawCookies))
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
	return url 
}

// Main API Handler
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	videoURL := r.URL.Query().Get("url")
	if videoURL == "" {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "URL parameter is missing"})
		return
	}

	videoID := extractVideoID(videoURL)
	targetURL := "https://m.youtube.com/watch?v=" + videoID

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Failed to create request"})
		return
	}

	// STRICT ANDROID HEADERS
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Ch-Ua-Platform", "\"Android\"")
	req.Header.Set("Sec-Ch-Ua-Mobile", "?1")

	if rawCookieHeader != "" {
		req.Header.Set("Cookie", rawCookieHeader)
	}

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

	// ---> FIXED: Ab cookie error ane par bhi raw_data bhejega <---
	if strings.Contains(bodyString, "problem with your cookie settings") {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Google rejected the cookies. Full HTML response in raw_data.",
			RawData: bodyString, // User ab yahan se dekh sakega ke Google ne kya bheja
		})
		return
	}

	// Regex for internal JSON data
	re := regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*({.+?});var`)
	match := re.FindStringSubmatch(bodyString)
	if len(match) < 2 {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Video JSON data not found in HTML. Check raw_data for full HTML.",
			RawData: bodyString, 
		})
		return
	}

	var playerData map[string]interface{}
	err = json.Unmarshal([]byte(match[1]), &playerData)
	if err != nil {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Failed to parse YouTube's JSON data. Raw JSON in raw_data.",
			RawData: match[1],
		})
		return
	}

	// Check Playability
	playability, _ := playerData["playabilityStatus"].(map[string]interface{})
	status, _ := playability["status"].(string)
	if status != "OK" {
		reason, _ := playability["reason"].(string)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Playability Error: " + reason,
			RawData: match[1],
		})
		return
	}

	// Extract Streaming Data
	streamingData, ok := playerData["streamingData"].(map[string]interface{})
	if !ok {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "No streamingData object found.",
			RawData: match[1],
		})
		return
	}

	var extractedFormats []VideoFormat

	processFormatList := func(formatList interface{}) {
		formats, ok := formatList.([]interface{})
		if !ok { return }
		for _, f := range formats {
			fmtData, ok := f.(map[string]interface{})
			if !ok { continue }
			
			quality, _ := fmtData["qualityLabel"].(string)
			if quality == "" { quality = "Audio Only" }
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

	processFormatList(streamingData["formats"])
	processFormatList(streamingData["adaptiveFormats"])

	if len(extractedFormats) == 0 {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Could not find direct URLs. Check raw_data for cipher check.",
			RawData: match[1],
		})
		return
	}

	// Final Success
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
	if port == "" { port = "8080" }

	fmt.Println("🚀 Go API Server running on port", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println("Server Error:", err)
	}
}

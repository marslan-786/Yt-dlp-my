package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	json.NewDecoder(file).Decode(&rawCookies)

	var parts []string
	hasConsent := false
	youtubeCookiesCount := 0

	for _, c := range rawCookies {
		if !strings.Contains(c.Domain, "youtube.com") {
			continue
		}
		cleanValue := strings.ReplaceAll(c.Value, "\n", "")
		cleanValue = strings.ReplaceAll(cleanValue, "\r", "")
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, cleanValue))
		youtubeCookiesCount++
		if strings.ToUpper(c.Name) == "CONSENT" {
			hasConsent = true
		}
	}
	
	if !hasConsent {
		parts = append(parts, "CONSENT=YES+cb.20210328-17-p0.en+FX+478")
		youtubeCookiesCount++
	}
	
	rawCookieHeader = strings.Join(parts, "; ")
	fmt.Printf("🍪 Filtered and Loaded %d STRICTLY YouTube Cookies!\n", youtubeCookiesCount)
}

func extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		return strings.Split(strings.Split(url, "v=")[1], "&")[0]
	} else if strings.Contains(url, "youtu.be/") {
		return strings.Split(strings.Split(url, "youtu.be/")[1], "?")[0]
	}
	return url 
}

// ---> 1. THE PROXY STREAMER (THE MAGIC FIX) <---
// Ye function IP Block wale masle ko khatam karega!
func proxyHandler(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("link")
	if targetURL == "" {
		http.Error(w, "Link parameter missing", http.StatusBadRequest)
		return
	}

	// Create request to the actual googlevideo.com link
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Pass necessary headers to act like a real browser downloading the file
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://www.youtube.com/")
	
	// Agar user ne partial video mangi hai (Seeking / Resuming), to Range header pass karo
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Proxy fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy important headers from YouTube back to the User (MP4 Type, Size, etc.)
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the video directly to the user like a pipe (0% RAM usage on server)
	io.Copy(w, resp.Body)
}

// ---> 2. MAIN DOWNLOAD HANDLER <---
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

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyString := string(bodyBytes)

	if strings.Contains(bodyString, "problem with your cookie settings") {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Google rejected the cookies."})
		return
	}

	re := regexp.MustCompile(`(?s)ytInitialPlayerResponse\s*=\s*(\{.+?\});(?:var\s|</script>)`)
	match := re.FindStringSubmatch(bodyString)
	if len(match) < 2 {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Video JSON data not found in HTML."})
		return
	}

	var playerData map[string]interface{}
	json.Unmarshal([]byte(match[1]), &playerData)

	playability, _ := playerData["playabilityStatus"].(map[string]interface{})
	status, _ := playability["status"].(string)
	if status != "OK" {
		reason, _ := playability["reason"].(string)
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Playability Error: " + reason})
		return
	}

	streamingData, ok := playerData["streamingData"].(map[string]interface{})
	if !ok {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "No streamingData object found."})
		return
	}

	var extractedFormats []VideoFormat
	host := r.Host // Get your server's domain/IP

	processFormatList := func(formatList interface{}) {
		formats, ok := formatList.([]interface{})
		if !ok { return }
		for _, f := range formats {
			fmtData, ok := f.(map[string]interface{})
			if !ok { continue }
			
			quality, _ := fmtData["qualityLabel"].(string)
			if quality == "" { quality = "Audio Only" }
			mime, _ := fmtData["mimeType"].(string)
			ytDirectUrl, _ := fmtData["url"].(string)

			if ytDirectUrl != "" {
				// ---> REPLACE GOOGLE URL WITH OUR PROXY URL <---
				// User ko apna API link do taake IP block na ho
				proxiedUrl := fmt.Sprintf("http://%s/proxy?link=%s", host, url.QueryEscape(ytDirectUrl))

				extractedFormats = append(extractedFormats, VideoFormat{
					Quality:  quality,
					MimeType: strings.Split(mime, ";")[0],
					URL:      proxiedUrl, 
				})
			}
		}
	}

	processFormatList(streamingData["formats"])
	processFormatList(streamingData["adaptiveFormats"])

	if len(extractedFormats) == 0 {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Could not find direct URLs."})
		return
	}

	json.NewEncoder(w).Encode(APIResponse{
		Status:  "success",
		VideoID: videoID,
		Formats: extractedFormats,
	})
}

func main() {
	loadCookies("youtube_cookies.json")

	http.HandleFunc("/api/download", downloadHandler)
	http.HandleFunc("/proxy", proxyHandler) // <-- NEW PROXY ROUTE ENABLED

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	fmt.Println("🚀 Go API Server running on port", port)
	http.ListenAndServe(":"+port, nil)
}

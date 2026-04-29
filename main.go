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
	Status        string        `json:"status"`
	VideoID       string        `json:"video_id,omitempty"`
	DownloadedURL string        `json:"downloaded_url,omitempty"` // ---> NEW: Server ka local link
	Formats       []VideoFormat `json:"formats,omitempty"`
	Message       string        `json:"message,omitempty"`
	RawData       string        `json:"raw_data,omitempty"`
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
		parts := strings.Split(url, "v=")
		return strings.Split(parts[1], "&")[0]
	} else if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		return strings.Split(parts[1], "?")[0]
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

	if strings.Contains(bodyString, "problem with your cookie settings") {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Google rejected the cookies.",
			RawData: bodyString, 
		})
		return
	}

	re := regexp.MustCompile(`(?s)ytInitialPlayerResponse\s*=\s*(\{.+?\});(?:var\s|</script>)`)
	match := re.FindStringSubmatch(bodyString)
	if len(match) < 2 {
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Video JSON data not found.",
		})
		return
	}

	var playerData map[string]interface{}
	json.Unmarshal([]byte(match[1]), &playerData)

	playability, _ := playerData["playabilityStatus"].(map[string]interface{})
	status, _ := playability["status"].(string)
	if status != "OK" {
		reason, _ := playability["reason"].(string)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error", 
			Message: "Playability Error: " + reason,
		})
		return
	}

	streamingData, ok := playerData["streamingData"].(map[string]interface{})
	if !ok {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "No streamingData object found."})
		return
	}

	var extractedFormats []VideoFormat
	var targetDownloadURL string

	processFormatList := func(formatList interface{}, isCombined bool) {
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
				// Sab se choti combined video (jis me audio+video dono hon) download karne ke liye save kar lo
				if isCombined && targetDownloadURL == "" {
					targetDownloadURL = url
				}
			}
		}
	}

	// Pehle 'formats' (combined audio/video) process karo, phir 'adaptive'
	processFormatList(streamingData["formats"], true)
	processFormatList(streamingData["adaptiveFormats"], false)

	if len(extractedFormats) == 0 {
		json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Could not find direct URLs."})
		return
	}

	// Agar combine format nahi mila to jo pehla link mile wahi utha lo
	if targetDownloadURL == "" {
		targetDownloadURL = extractedFormats[0].URL
	}

	// ---> START SERVER-SIDE DOWNLOADING <---
	fmt.Println("⏳ Downloading smallest video chunk to server...")
	os.MkdirAll("downloads", os.ModePerm) // Downloads folder create karega
	fileName := videoID + ".mp4"
	filePath := "downloads/" + fileName

	dlReq, _ := http.NewRequest("GET", targetDownloadURL, nil)
	dlReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	
	dlClient := &http.Client{Timeout: 60 * time.Second} // Max 60 sec wait karega download ka
	dlResp, err := dlClient.Do(dlReq)
	
	localDownloadURL := ""
	if err == nil && dlResp.StatusCode == 200 {
		defer dlResp.Body.Close()
		outFile, err := os.Create(filePath)
		if err == nil {
			io.Copy(outFile, dlResp.Body)
			outFile.Close()
			fmt.Println("✅ Video downloaded successfully:", fileName)
			// Local URL generate kar raha hai (e.g. http://localhost:8080/downloads/video_id.mp4)
			localDownloadURL = fmt.Sprintf("http://%s/downloads/%s", r.Host, fileName)
		}
	} else {
		fmt.Println("❌ Failed to download video to server.")
	}
	// ----------------------------------------

	// Send Final Success Response
	json.NewEncoder(w).Encode(APIResponse{
		Status:        "success",
		VideoID:       videoID,
		DownloadedURL: localDownloadURL, // Seedha server ka direct link!
		Formats:       extractedFormats,
	})
}

func main() {
	loadCookies("youtube_cookies.json")

	// Routes
	http.HandleFunc("/api/download", downloadHandler)
	
	// Naya route banaya hai jo downloads folder ki files serve karega
	http.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("downloads"))))

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }

	fmt.Println("🚀 Go API Server running on port", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println("Server Error:", err)
	}
}

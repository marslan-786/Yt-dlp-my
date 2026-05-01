package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// YTDLP JSON کو پارس کرنے کے لیے سٹرکچرز
type YTDLPFormat struct {
	Language string `json:"language"`
	Vcodec   string `json:"vcodec"`
	Acodec   string `json:"acodec"`
}

type YTDLPInfo struct {
	Formats []YTDLPFormat `json:"formats"`
}

type APIRequest struct {
	URL string `json:"url"`
}

type APIResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
}

func main() {
	// Root روٹ جس پر HTML شو ہوگا
	http.HandleFunc("/", serveHTML)
	// API روٹ جو پروسیسنگ کرے گا
	http.HandleFunc("/api/process", processVideo)

	fmt.Println("🚀 Server running on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	html := `
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>YouTube Hindi Downloader API</title>
		<style>
			body { font-family: Arial, sans-serif; background: #121212; color: #fff; text-align: center; padding: 50px; }
			.container { max-width: 500px; margin: 0 auto; background: #1e1e1e; padding: 30px; border-radius: 10px; box-shadow: 0 4px 10px rgba(0,0,0,0.5); }
			input { width: 90%; padding: 12px; margin-bottom: 20px; border: 1px solid #333; border-radius: 5px; background: #2a2a2a; color: white; }
			button { width: 95%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 5px; cursor: pointer; font-size: 16px; font-weight: bold; }
			button:disabled { background: #555; cursor: not-allowed; }
			#downloadBtn { display: none; background: #28a745; margin-top: 10px; text-decoration: none; padding: 12px; border-radius: 5px; color: white; font-weight: bold; }
			.console { margin-top: 20px; background: #000; color: #0f0; padding: 15px; border-radius: 5px; text-align: left; font-family: monospace; font-size: 14px; min-height: 50px; white-space: pre-wrap; display: none; border: 1px solid #333; }
			.error-text { color: #ff4c4c; }
		</style>
	</head>
	<body>
		<div class="container">
			<h2>🎥 Hindi Video Fetcher</h2>
			<input type="text" id="urlInput" placeholder="Enter YouTube URL here...">
			<button id="startBtn">Start Processing</button>
			<a href="#" id="downloadBtn" target="_blank">⬇️ Download Video</a>
			<div id="consoleBox" class="console"></div>
		</div>

		<script>
			const startBtn = document.getElementById('startBtn');
			const downloadBtn = document.getElementById('downloadBtn');
			const consoleBox = document.getElementById('consoleBox');
			const urlInput = document.getElementById('urlInput');

			function logMsg(msg, isError = false) {
				consoleBox.style.display = "block";
				if(isError) {
					consoleBox.innerHTML += "<span class='error-text'>[ERROR] " + msg + "</span><br>";
				} else {
					consoleBox.innerHTML += "[INFO] " + msg + "<br>";
				}
				consoleBox.scrollTop = consoleBox.scrollHeight;
			}

			startBtn.addEventListener('click', async () => {
				const url = urlInput.value.trim();
				if(!url) return alert("Please enter a link!");

				startBtn.disabled = true;
				startBtn.innerText = "Processing...";
				downloadBtn.style.display = "none";
				consoleBox.innerHTML = "";
				logMsg("Fetching video details to check languages...");

				try {
					const res = await fetch('/api/process', {
						method: 'POST',
						headers: { 'Content-Type': 'application/json' },
						body: JSON.stringify({ url: url })
					});
					
					const data = await res.json();
					
					if (data.status === "success") {
						logMsg("✅ Upload complete! URL generated.");
						startBtn.style.display = "none";
						downloadBtn.style.display = "block";
						downloadBtn.href = data.url;
					} else {
						logMsg(data.message, true);
						startBtn.disabled = false;
						startBtn.innerText = "Start Processing";
					}
				} catch (err) {
					logMsg(err.message, true);
					startBtn.disabled = false;
					startBtn.innerText = "Start Processing";
				}
			});
		</script>
	</body>
	</html>
	`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func processVideo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req APIRequest
	json.NewDecoder(r.Body).Decode(&req)

	if req.URL == "" {
		sendJSONResponse(w, "error", "URL is missing!", "")
		return
	}

	// Step 1: ویڈیو کا JSON ڈیٹا نکالنا تاکہ آڈیو ٹریکس چیک کیے جا سکیں
	cmdJSON := exec.Command("yt-dlp", "-J", req.URL)
	var stdout bytes.Buffer
	cmdJSON.Stdout = &stdout
	err := cmdJSON.Run()

	if err != nil {
		sendJSONResponse(w, "error", "Failed to fetch video info. It might be restricted or invalid.", "")
		return
	}

	var info YTDLPInfo
	json.Unmarshal(stdout.Bytes(), &info)

	// Step 2: تمام دستیاب زبانوں کی لسٹ بنانا
	languages := make(map[string]bool)
	hasHindi := false

	for _, format := range info.Formats {
		if format.Language != "" && format.Language != "null" {
			langLower := strings.ToLower(format.Language)
			languages[langLower] = true
			
			// ہندی چیک کرنا (hi یا hin)
			if strings.Contains(langLower, "hi") || strings.Contains(langLower, "hin") {
				hasHindi = true
			}
		}
	}

	// اگر ہندی نہ ملے تو دستیاب زبانوں کی لسٹ ریٹرن کر دو
	if !hasHindi {
		var availableLangs []string
		for lang := range languages {
			availableLangs = append(availableLangs, lang)
		}
		langStr := strings.Join(availableLangs, ", ")
		if langStr == "" {
			langStr = "No distinct audio tracks found."
		}
		
		errMsg := fmt.Sprintf("❌ ہندی آڈیو ٹریک نہیں ملا!\n\nاس ویڈیو میں یہ زبانیں دستیاب ہیں:\n%s\n\n(اگر آپ کو لگتا ہے کہ ہندی موجود ہے تو وہ یوٹیوب پر کسی اور کوڈ سے سیو ہوگی جو اوپر لسٹ میں ہے۔)", langStr)
		sendJSONResponse(w, "error", errMsg, "")
		return
	}

	// Step 3: اگر ہندی مل گئی تو اب سختی سے ڈاؤن لوڈ کرو
	tempDir, _ := os.MkdirTemp("", "ytdlp_*")
	defer os.RemoveAll(tempDir)

	outputTemplate := filepath.Join(tempDir, "%(id)s.%(ext)s")
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// Strictly force Hindi formats
	formatString := "bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hi]/bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hin]"

	cmdDL := exec.Command("yt-dlp",
		"--no-warnings",
		"--no-playlist",
		"--merge-output-format", "mp4",
		"-f", formatString,
		"--user-agent", userAgent,
		"--postprocessor-args", "ffmpeg:-c:v libx264 -pix_fmt yuv420p -c:a aac -movflags +faststart",
		"--output", outputTemplate,
		req.URL,
	)

	var stderr bytes.Buffer
	cmdDL.Stderr = &stderr
	err = cmdDL.Run()

	if err != nil {
		sendJSONResponse(w, "error", fmt.Sprintf("Download Failed:\n%s", stderr.String()), "")
		return
	}

	// Step 4: فائنل .mp4 فائل ڈھونڈنا اور اپلوڈ کرنا
	files, _ := os.ReadDir(tempDir)
	var downloadedPath string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".mp4") {
			downloadedPath = filepath.Join(tempDir, f.Name())
			break
		}
	}

	if downloadedPath == "" {
		sendJSONResponse(w, "error", "File downloaded but could not be located.", "")
		return
	}

	// Step 5: آپ کی کیٹ باکس API پر اپلوڈ کرنا
	uploadedURL, err := uploadToCatbox(downloadedPath)
	if err != nil {
		sendJSONResponse(w, "error", fmt.Sprintf("Upload Failed: %v", err), "")
		return
	}

	// Success! Return final URL
	sendJSONResponse(w, "success", "Download and upload complete!", uploadedURL)
}

// JSON رسپانس بھیجنے کا ہیلپر فنکشن
func sendJSONResponse(w http.ResponseWriter, status, msg, url string) {
	resp := APIResponse{Status: status, Message: msg, URL: url}
	json.NewEncoder(w).Encode(resp)
}

// کیٹ باکس کلون پر اپلوڈ کرنے کا فنکشن
func uploadToCatbox(filePath string) (string, error) {
	fileToUpload, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer fileToUpload.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("reqtype", "fileupload")
	
	part, err := writer.CreateFormFile("fileToUpload", filepath.Base(filePath))
	if err == nil {
		_, _ = io.Copy(part, fileToUpload)
	}
	writer.Close()

	req, err := http.NewRequest("POST", "https://catbox-production-6705.up.railway.app/upload", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Server returned %d: %s", resp.StatusCode, string(respBytes))
	}

	return strings.TrimSpace(string(respBytes)), nil
}

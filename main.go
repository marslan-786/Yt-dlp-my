package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

const downloadsDir = "./downloads"

func main() {
	// سرور سٹارٹ ہونے سے پہلے ڈاؤن لوڈز کا فولڈر بنا لیں
	os.MkdirAll(downloadsDir, os.ModePerm)

	// API اور HTML روٹس
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/api/process", processVideo)

	// ویڈیوز کو ڈائریکٹ سرو کرنے کے لیے Static File Server
	http.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir(downloadsDir))))

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
			.console { margin-top: 20px; background: #000; color: #0f0; padding: 15px; border-radius: 5px; text-align: left; font-family: monospace; font-size: 14px; min-height: 50px; white-space: pre-wrap; display: none; border: 1px solid #333; overflow-x: auto; }
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
					consoleBox.innerHTML += "<span class='error-text'>[ERROR] " + msg + "</span><br><br>";
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
						logMsg("✅ Video is ready on our server!");
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

	// 📱 FULL ANDROID HEADERS TO BYPASS PO TOKEN
	userAgent := "Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.90 Mobile Safari/537.36"
	clientExtractor := "youtube:client=android,android_creator"

	// Step 1: ویڈیو کا JSON ڈیٹا نکالنا (اصل ایرر کیچ کرنے کے ساتھ)
	cmdJSON := exec.Command("yt-dlp",
		"-J",
		"--extractor-args", clientExtractor,
		"--user-agent", userAgent,
		req.URL,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmdJSON.Stdout = &stdout
	cmdJSON.Stderr = &stderr
	err := cmdJSON.Run()

	if err != nil {
		errMsg := fmt.Sprintf("yt-dlp Error fetching info:\n%s", strings.TrimSpace(stderr.String()))
		sendJSONResponse(w, "error", errMsg, "")
		return
	}

	var info YTDLPInfo
	json.Unmarshal(stdout.Bytes(), &info)

	// Step 2: ہندی زبان کی موجودگی چیک کرنا
	languages := make(map[string]bool)
	hasHindi := false

	for _, format := range info.Formats {
		if format.Language != "" && format.Language != "null" {
			langLower := strings.ToLower(format.Language)
			languages[langLower] = true
			
			if strings.Contains(langLower, "hi") || strings.Contains(langLower, "hin") {
				hasHindi = true
			}
		}
	}

	if !hasHindi {
		var availableLangs []string
		for lang := range languages {
			availableLangs = append(availableLangs, lang)
		}
		langStr := strings.Join(availableLangs, ", ")
		if langStr == "" {
			langStr = "No distinct audio tracks found."
		}
		
		errMsg := fmt.Sprintf("ہندی آڈیو ٹریک نہیں ملا!\nاس ویڈیو میں یہ زبانیں دستیاب ہیں: [ %s ]", langStr)
		sendJSONResponse(w, "error", errMsg, "")
		return
	}

	// Step 3: ڈاؤن لوڈ کے لیے ایک یونیک فولڈر بنانا تاکہ فائلز مکس نہ ہوں
	reqID := fmt.Sprintf("%d", time.Now().UnixNano())
	targetDir := filepath.Join(downloadsDir, reqID)
	os.MkdirAll(targetDir, os.ModePerm)

	outputTemplate := filepath.Join(targetDir, "%(id)s.%(ext)s")
	formatString := "bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hi]/bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hin]"

	// Step 4: ڈاؤن لوڈ کمانڈ رن کرنا
	cmdDL := exec.Command("yt-dlp",
		"--no-warnings",
		"--no-playlist",
		"--merge-output-format", "mp4",
		"-f", formatString,
		"--extractor-args", clientExtractor,
		"--user-agent", userAgent,
		"--postprocessor-args", "ffmpeg:-c:v libx264 -pix_fmt yuv420p -c:a aac -movflags +faststart",
		"--output", outputTemplate,
		req.URL,
	)

	var dlStderr bytes.Buffer
	cmdDL.Stderr = &dlStderr
	err = cmdDL.Run()

	if err != nil {
		sendJSONResponse(w, "error", fmt.Sprintf("Download Failed:\n%s", strings.TrimSpace(dlStderr.String())), "")
		return
	}

	// Step 5: ڈاؤن لوڈ کی گئی فائل کا نام ڈھونڈنا اور لوکل URL بنانا
	files, _ := os.ReadDir(targetDir)
	var downloadedFile string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".mp4") {
			downloadedFile = f.Name()
			break
		}
	}

	if downloadedFile == "" {
		sendJSONResponse(w, "error", "File downloaded but could not be located on server.", "")
		return
	}

	// فائنل URL جو سیدھا آپ کے سرور کے /downloads/ فولڈر کو پوائنٹ کرے گا
	finalURL := fmt.Sprintf("/downloads/%s/%s", reqID, downloadedFile)

	// Success! Return final URL
	sendJSONResponse(w, "success", "Download complete!", finalURL)
}

func sendJSONResponse(w http.ResponseWriter, status, msg, url string) {
	resp := APIResponse{Status: status, Message: msg, URL: url}
	json.NewEncoder(w).Encode(resp)
}

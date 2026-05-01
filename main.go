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
	FormatID   string `json:"format_id"`
	Ext        string `json:"ext"`
	Language   string `json:"language"`
	Resolution string `json:"resolution"`
	Vcodec     string `json:"vcodec"`
	Acodec     string `json:"acodec"`
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
const cookiesFile = "cookies.txt" 

func main() {
	os.MkdirAll(downloadsDir, os.ModePerm)

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/api/process", processVideo)

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
			.console { margin-top: 20px; background: #000; color: #0f0; padding: 15px; border-radius: 5px; text-align: left; font-family: monospace; font-size: 14px; min-height: 50px; white-space: pre-wrap; display: none; border: 1px solid #333; overflow-x: auto; max-height: 300px; overflow-y: auto;}
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
					consoleBox.innerHTML += "<span class='error-text'>[ERROR]\n" + msg + "</span><br><br>";
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
				logMsg("Fetching video details to check languages & formats...");

				try {
					const res = await fetch('/api/process', {
						method: 'POST',
						headers: { 'Content-Type': 'application/json' },
						body: JSON.stringify({ url: url })
					});
					
					const data = await res.json();
					
					if (data.status === "success") {
						logMsg(data.message); // یہ بتائے گا کونسا کلائنٹ کامیاب ہوا
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

	// ---------------------------------------------------------
	// 🔥 FALLBACK CONFIGURATIONS (SMART CLIENT ROTATION)
	// ---------------------------------------------------------
	clientConfigs := []struct {
		client string
		ua     string
		cookie bool
	}{
		{"tv", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36", true},
		{"web", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36", true},
		{"ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1", true},
		{"android,android_creator", "Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.90 Mobile Safari/537.36", false}, // Android without cookies
	}

	var info YTDLPInfo
	var successfulClient string
	var successfulUA string
	var usedCookies bool
	var lastErr string
	fetchSuccess := false

	// Step 1: باری باری ہر کلائنٹ کو ٹرائی کرنا
	for _, cfg := range clientConfigs {
		args := []string{"-J"}
		
		// کوکیز کی شرط
		if cfg.cookie {
			args = append(args, "--cookies", cookiesFile)
		}
		
		args = append(args, "--extractor-args", "youtube:client="+cfg.client)
		args = append(args, "--extractor-args", "youtube:lang=hi")
		args = append(args, "--user-agent", cfg.ua)
		args = append(args, req.URL)

		cmdJSON := exec.Command("yt-dlp", args...)
		var stdout, stderr bytes.Buffer
		cmdJSON.Stdout = &stdout
		cmdJSON.Stderr = &stderr
		
		err := cmdJSON.Run()

		if err == nil {
			errParse := json.Unmarshal(stdout.Bytes(), &info)
			// اگر JSON ٹھیک پارس ہو جائے اور کم از کم ایک فارمیٹ مل جائے تو لوپ بریک کر دو
			if errParse == nil && len(info.Formats) > 0 {
				successfulClient = cfg.client
				successfulUA = cfg.ua
				usedCookies = cfg.cookie
				fetchSuccess = true
				break
			}
		}
		// آخری ایرر سنبھال کر رکھنا تاکہ اگر سب فیل ہوں تو فرنٹ اینڈ کو دکھایا جا سکے
		lastErr = strings.TrimSpace(stderr.String())
	}

	// اگر سارے کلائنٹس فیل ہو جائیں
	if !fetchSuccess {
		errMsg := fmt.Sprintf("yt-dlp Fallback Failed. تمام کلائنٹس ناکام ہو گئے۔\nآخری ایرر:\n%s\n\n⚠️ نوٹ: چیک کریں کہ کوکیز کام کر رہی ہیں یا نہیں۔", lastErr)
		sendJSONResponse(w, "error", errMsg, "")
		return
	}

	// Step 2: ہندی زبان چیک کرنا اور تمام دستیاب فارمیٹس کو لسٹ کرنا
	languages := make(map[string]bool)
	var formatList []string
	hasHindi := false

	for _, format := range info.Formats {
		if format.Vcodec != "mjpeg" {
			langDisplay := format.Language
			if langDisplay == "" || langDisplay == "null" {
				langDisplay = "unknown"
			} else {
				langLower := strings.ToLower(langDisplay)
				languages[langLower] = true
				if strings.Contains(langLower, "hi") || strings.Contains(langLower, "hin") {
					hasHindi = true
				}
			}

			resDisplay := format.Resolution
			if resDisplay == "audio only" || resDisplay == "" {
				resDisplay = "Audio"
			}
			fmtDetail := fmt.Sprintf("- [%s] %s | Ext: %s | Lang: %s", format.FormatID, resDisplay, format.Ext, langDisplay)
			formatList = append(formatList, fmtDetail)
		}
	}

	if !hasHindi {
		var availableLangs []string
		for lang := range languages {
			availableLangs = append(availableLangs, lang)
		}
		langStr := strings.Join(availableLangs, ", ")
		if langStr == "" {
			langStr = "No distinct audio languages found."
		}

		formatsStr := strings.Join(formatList, "\n")
		
		errMsg := fmt.Sprintf("❌ ہندی آڈیو ٹریک نہیں ملا!\n(Passed Client: %s)\n\n🌍 دستیاب زبانیں:\n[ %s ]\n\n📜 دستیاب فارمیٹس کی لسٹ:\n%s", successfulClient, langStr, formatsStr)
		sendJSONResponse(w, "error", errMsg, "")
		return
	}

	// Step 3: ڈاؤن لوڈ کے لیے ایک یونیک فولڈر بنانا
	reqID := fmt.Sprintf("%d", time.Now().UnixNano())
	targetDir := filepath.Join(downloadsDir, reqID)
	os.MkdirAll(targetDir, os.ModePerm)

	outputTemplate := filepath.Join(targetDir, "%(id)s.%(ext)s")
	formatString := "bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hi]/bestvideo[height<=360][vcodec^=avc1]+bestaudio[language*=hin]"

	// Step 4: فائنل ڈاؤن لوڈ (وہی کلائنٹ استعمال کرنا جو Step 1 میں کامیاب ہوا تھا)
	dlArgs := []string{
		"--no-warnings",
		"--no-playlist",
		"--merge-output-format", "mp4",
		"-f", formatString,
	}
	
	if usedCookies {
		dlArgs = append(dlArgs, "--cookies", cookiesFile)
	}
	
	dlArgs = append(dlArgs, "--extractor-args", "youtube:client="+successfulClient)
	dlArgs = append(dlArgs, "--extractor-args", "youtube:lang=hi")
	dlArgs = append(dlArgs, "--user-agent", successfulUA)
	dlArgs = append(dlArgs, "--postprocessor-args", "ffmpeg:-c:v libx264 -pix_fmt yuv420p -c:a aac -movflags +faststart")
	dlArgs = append(dlArgs, "--output", outputTemplate)
	dlArgs = append(dlArgs, req.URL)

	cmdDL := exec.Command("yt-dlp", dlArgs...)

	var dlStderr bytes.Buffer
	cmdDL.Stderr = &dlStderr
	err := cmdDL.Run()

	if err != nil {
		sendJSONResponse(w, "error", fmt.Sprintf("Download Failed on client '%s':\n%s", successfulClient, strings.TrimSpace(dlStderr.String())), "")
		return
	}

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

	finalURL := fmt.Sprintf("/downloads/%s/%s", reqID, downloadedFile)
	
	// فرنٹ اینڈ پر بتانا کہ کونسا کلائنٹ کامیاب رہا تھا
	successMsg := fmt.Sprintf("Download complete! (Used Client: %s)", successfulClient)
	sendJSONResponse(w, "success", successMsg, finalURL)
}

func sendJSONResponse(w http.ResponseWriter, status, msg, url string) {
	resp := APIResponse{Status: status, Message: msg, URL: url}
	json.NewEncoder(w).Encode(resp)
}

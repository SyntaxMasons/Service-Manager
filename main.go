package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var appConfig Config

type Config struct {
	Tokens           []string `json:"tokens"`
	AllowedSystemctl []string `json:"allowed_systemctl"`
	AllowedDocker    []string `json:"allowed_docker"`
	AllowedNerdctl   []string `json:"allowed_nerdctl"`
}

type Response struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Running *bool  `json:"is_running,omitempty"`
	Raw     string `json:"raw_status,omitempty"`
}

type SystemStats struct {
	CPUUsage  string `json:"cpu_usage"`
	RAMTotal  string `json:"ram_total"`
	RAMUsed   string `json:"ram_used"`
	VRAMTotal string `json:"vram_total"`
	VRAMUsed  string `json:"vram_used"`
}

func loadConfig(filename string) {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}
	if err := json.Unmarshal(file, &appConfig); err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func isAuthorized(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return false
	}
	return contains(appConfig.Tokens, parts[1])
}

func sendJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthorized(r) {
		sendJSON(w, http.StatusUnauthorized, Response{Error: "Unauthorized. Invalid or missing token."})
		return
	}

	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Only GET is supported"})
		return
	}

	stats := SystemStats{
		CPUUsage:  "N/A",
		RAMTotal:  "N/A",
		RAMUsed:   "N/A",
		VRAMTotal: "N/A",
		VRAMUsed:  "N/A",
	}

	ramOut, err := runCommand("bash", "-c", "free -m | awk 'NR==2{print $3, $2}'")
	if err == nil {
		parts := strings.Fields(ramOut)
		if len(parts) == 2 {
			stats.RAMUsed = parts[0] + " MB"
			stats.RAMTotal = parts[1] + " MB"
		}
	}

	cpuOut, err := runCommand("bash", "-c", "top -bn1 | grep -i 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1\"%\"}'")
	if err == nil && cpuOut != "" {
		stats.CPUUsage = cpuOut
	}

	vramOut, err := runCommand("bash", "-c", "nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader | head -n 1")
	if err == nil && vramOut != "" && !strings.Contains(strings.ToLower(vramOut), "not found") {
		parts := strings.Split(vramOut, ",")
		if len(parts) == 2 {
			stats.VRAMUsed = strings.TrimSpace(parts[0])
			stats.VRAMTotal = strings.TrimSpace(parts[1])
		}
	}

	sendJSON(w, http.StatusOK, stats)
}

func serviceHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthorized(r) {
		sendJSON(w, http.StatusUnauthorized, Response{Error: "Unauthorized. Invalid or missing token."})
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 4 || pathParts[0] != "service" {
		sendJSON(w, http.StatusNotFound, Response{Error: "Format must be /service/<type>/<name>/<action>"})
		return
	}

	svcType, svcName, action := pathParts[1], pathParts[2], pathParts[3]

	if !validNameRegex.MatchString(svcName) {
		sendJSON(w, http.StatusBadRequest, Response{Error: "Invalid service name format"})
		return
	}

	if svcType == "systemctl" && !contains(appConfig.AllowedSystemctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Service '%s' is not whitelisted", svcName)})
		return
	} else if svcType == "docker" && !contains(appConfig.AllowedDocker, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Container '%s' is not whitelisted", svcName)})
		return
	} else if svcType == "nerdctl" && !contains(appConfig.AllowedNerdctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Container '%s' is not whitelisted", svcName)})
		return
	} else if svcType != "systemctl" && svcType != "docker" && svcType != "nerdctl" {
		sendJSON(w, http.StatusBadRequest, Response{Error: "Unsupported type. Use 'systemctl', 'docker', or 'nerdctl'"})
		return
	}

	if r.Method == http.MethodGet {
		if action != "status" {
			sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Only /status is supported for GET"})
			return
		}
		isRunning, rawStatus := false, ""
		if svcType == "systemctl" {
			out, err := runCommand("sudo", "systemctl", "is-active", svcName)
			isRunning = (err == nil && out == "active")
			rawStatus = out
		} else {
			out, err := runCommand(svcType, "inspect", "-f", "{{.State.Running}}", svcName)
			if err != nil {
				sendJSON(w, http.StatusNotFound, Response{Error: "Container not found or inaccessible"})
				return
			}
			isRunning = (out == "true")
			rawStatus = out
		}
		sendJSON(w, http.StatusOK, Response{Running: &isRunning, Raw: rawStatus})
		return
	}

	if r.Method == http.MethodPost {
		if action != "start" && action != "stop" {
			sendJSON(w, http.StatusBadRequest, Response{Error: "Action must be 'start' or 'stop'"})
			return
		}
		var err error
		var out string
		if svcType == "systemctl" {
			out, err = runCommand("sudo", "systemctl", action, svcName)
		} else {
			out, err = runCommand(svcType, action, svcName)
		}
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, Response{Error: fmt.Sprintf("Command failed: %s", out)})
			return
		}
		sendJSON(w, http.StatusOK, Response{Status: "success", Message: fmt.Sprintf("%s %sed successfully", svcName, action)})
		return
	}
	sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Method not allowed"})
}

// Swagger UI HTML with Embedded OpenAPI JSON
func docsHandler(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>API Docs - Service Manager</title>
  <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/4.18.3/swagger-ui.css" />
  <style> body { margin: 0; padding: 0; } </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/4.18.3/swagger-ui-bundle.js"></script>
  <script>
    const spec = {
      "openapi": "3.0.0",
      "info": {
        "title": "Service Manager",
        "version": "1.0.0",
        "description": "Securely manage whitelisted systemctl, docker, and nerdctl services."
      },
      "components": {
        "securitySchemes": {
          "bearerAuth": { "type": "http", "scheme": "bearer" }
        }
      },
      "security": [ { "bearerAuth": [] } ],
      "paths": {
        "/system/stats": {
          "get": {
            "tags": ["System"],
            "summary": "Get system stats (CPU, RAM, VRAM)",
            "responses": { "200": { "description": "Successful response" } }
          }
        },
        "/service/{type}/{name}/status": {
          "get": {
            "tags": ["Services"],
            "summary": "Check service status",
            "parameters": [
              { "name": "type", "in": "path", "required": true, "schema": { "type": "string", "enum": ["systemctl", "docker", "nerdctl"] } },
              { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } }
            ],
            "responses": { "200": { "description": "Successful response" } }
          }
        },
        "/service/{type}/{name}/start": {
          "post": {
            "tags": ["Services"],
            "summary": "Start a service",
            "parameters": [
              { "name": "type", "in": "path", "required": true, "schema": { "type": "string", "enum": ["systemctl", "docker", "nerdctl"] } },
              { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } }
            ],
            "responses": { "200": { "description": "Successful response" } }
          }
        },
        "/service/{type}/{name}/stop": {
          "post": {
            "tags": ["Services"],
            "summary": "Stop a service",
            "parameters": [
              { "name": "type", "in": "path", "required": true, "schema": { "type": "string", "enum": ["systemctl", "docker", "nerdctl"] } },
              { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } }
            ],
            "responses": { "200": { "description": "Successful response" } }
          }
        }
      }
    };
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        spec: spec,
        dom_id: '#swagger-ui',
        deepLinking: true,
      });
    };
  </script>
</body>
</html>`
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func main() {
	loadConfig("config.json")
	
	http.HandleFunc("/service/", serviceHandler)
	http.HandleFunc("/system/stats", statsHandler)
	http.HandleFunc("/docs", docsHandler) // New Swagger UI endpoint

	port := ":8000"
	fmt.Printf("Starting secure API at http://127.0.0.1%s...\n", port)
	fmt.Printf("Swagger API Docs available at http://127.0.0.1%s/docs\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
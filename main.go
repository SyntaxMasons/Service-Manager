package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var appConfig Config

type WorkflowStep struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

type Config struct {
	Port             int                       `json:"port"` // Added port configuration
	Tokens           []string                  `json:"tokens"`
	AllowedSystemctl []string                  `json:"allowed_systemctl"`
	AllowedDocker    []string                  `json:"allowed_docker"`
	AllowedNerdctl   []string                  `json:"allowed_nerdctl"`
	Workflows        map[string][]WorkflowStep `json:"workflows"`
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

// -------------------------------------------------------------------
// LOGGING MIDDLEWARE
// -------------------------------------------------------------------
func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("INCOMING | %s | %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next(w, r)
		log.Printf("COMPLETED | %s | %s %s | %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
	}
}

func loadConfig(filename string) {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("FATAL: Error reading config file: %v", err)
	}
	if err := json.Unmarshal(file, &appConfig); err != nil {
		log.Fatalf("FATAL: Error parsing config file: %v", err)
	}
	
	// Set default port if not provided in config
	if appConfig.Port == 0 {
		appConfig.Port = 8000
	}
	
	log.Printf("INFO: Configuration loaded successfully. Workflows found: %d", len(appConfig.Workflows))
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
		log.Printf("WARN: Unauthorized access attempt from %s (Missing Token)", r.RemoteAddr)
		return false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		log.Printf("WARN: Unauthorized access attempt from %s (Malformed Header)", r.RemoteAddr)
		return false
	}
	isValid := contains(appConfig.Tokens, parts[1])
	if !isValid {
		log.Printf("WARN: Unauthorized access attempt from %s (Invalid Token)", r.RemoteAddr)
	}
	return isValid
}

func sendJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func runCommand(name string, args ...string) (string, error) {
	log.Printf("EXEC: %s %s", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ERROR: Command failed: %s | Output: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), err
}

func waitForState(svcType string, svcName string, expectedRunning bool) error {
	maxRetries := 30
	log.Printf("INFO: Waiting for %s '%s' to reach expected state (Running=%v)...", svcType, svcName, expectedRunning)

	for i := 0; i < maxRetries; i++ {
		isRunning := false
		if svcType == "systemctl" {
			out, err := runCommand("sudo", "systemctl", "is-active", svcName)
			isRunning = (err == nil && out == "active")
		} else {
			out, err := runCommand(svcType, "inspect", "-f", "{{.State.Running}}", svcName)
			isRunning = (err == nil && out == "true")
		}

		if isRunning == expectedRunning {
			log.Printf("INFO: %s '%s' reached expected state.", svcType, svcName)
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	stateStr := "stop"
	if expectedRunning {
		stateStr = "start"
	}
	return fmt.Errorf("timeout (30s) waiting for %s to %s completely", svcName, stateStr)
}

// -------------------------------------------------------------------
// HANDLERS
// -------------------------------------------------------------------

func statsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthorized(r) {
		sendJSON(w, http.StatusUnauthorized, Response{Error: "Unauthorized."})
		return
	}
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Only GET is supported"})
		return
	}

	stats := SystemStats{CPUUsage: "N/A", RAMTotal: "N/A", RAMUsed: "N/A", VRAMTotal: "N/A", VRAMUsed: "N/A"}

	if ramOut, err := runCommand("bash", "-c", "free -m | awk 'NR==2{print $3, $2}'"); err == nil {
		parts := strings.Fields(ramOut)
		if len(parts) == 2 {
			stats.RAMUsed = parts[0] + " MB"
			stats.RAMTotal = parts[1] + " MB"
		}
	}
	if cpuOut, err := runCommand("bash", "-c", "top -bn1 | grep -i 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1\"%\"}'"); err == nil && cpuOut != "" {
		stats.CPUUsage = cpuOut
	}
	if vramOut, err := runCommand("bash", "-c", "nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader | head -n 1"); err == nil && vramOut != "" && !strings.Contains(strings.ToLower(vramOut), "not found") {
		parts := strings.Split(vramOut, ",")
		if len(parts) == 2 {
			stats.VRAMUsed = strings.TrimSpace(parts[0])
			stats.VRAMTotal = strings.TrimSpace(parts[1])
		}
	}
	sendJSON(w, http.StatusOK, stats)
}

func workflowHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthorized(r) {
		sendJSON(w, http.StatusUnauthorized, Response{Error: "Unauthorized."})
		return
	}
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Only POST is supported for workflows"})
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 2 || pathParts[0] != "workflow" {
		sendJSON(w, http.StatusNotFound, Response{Error: "Format must be /workflow/<name>"})
		return
	}

	wfName := pathParts[1]
	steps, exists := appConfig.Workflows[wfName]
	if !exists {
		sendJSON(w, http.StatusNotFound, Response{Error: fmt.Sprintf("Workflow '%s' not found in config", wfName)})
		return
	}

	log.Printf("WORKFLOW: Starting workflow '%s' with %d steps", wfName, len(steps))

	for i, step := range steps {
		log.Printf("WORKFLOW: Step %d/%d -> [%s] %s %s", i+1, len(steps), step.Type, step.Action, step.Name)

		if !validNameRegex.MatchString(step.Name) || (step.Action != "start" && step.Action != "stop") {
			errStr := fmt.Sprintf("Invalid step config at index %d", i)
			log.Printf("ERROR: %s", errStr)
			sendJSON(w, http.StatusBadRequest, Response{Error: errStr})
			return
		}

		if step.Type == "systemctl" && !contains(appConfig.AllowedSystemctl, step.Name) {
			sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Step %d Failed: Service '%s' is not whitelisted", i, step.Name)})
			return
		} else if step.Type == "docker" && !contains(appConfig.AllowedDocker, step.Name) {
			sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Step %d Failed: Container '%s' is not whitelisted", i, step.Name)})
			return
		} else if step.Type == "nerdctl" && !contains(appConfig.AllowedNerdctl, step.Name) {
			sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Step %d Failed: Container '%s' is not whitelisted", i, step.Name)})
			return
		}

		var err error
		if step.Type == "systemctl" {
			_, err = runCommand("sudo", "systemctl", step.Action, step.Name)
		} else {
			_, err = runCommand(step.Type, step.Action, step.Name)
		}

		if err != nil {
			errStr := fmt.Sprintf("Workflow aborted at step %d (%s %s)", i+1, step.Action, step.Name)
			log.Printf("ERROR: %s", errStr)
			sendJSON(w, http.StatusInternalServerError, Response{Error: errStr})
			return
		}

		expectedState := (step.Action == "start")
		if waitErr := waitForState(step.Type, step.Name, expectedState); waitErr != nil {
			errStr := fmt.Sprintf("Workflow aborted at step %d: %v", i+1, waitErr)
			log.Printf("ERROR: %s", errStr)
			sendJSON(w, http.StatusInternalServerError, Response{Error: errStr})
			return
		}
	}

	log.Printf("WORKFLOW: Successfully completed '%s'", wfName)
	sendJSON(w, http.StatusOK, Response{Status: "success", Message: fmt.Sprintf("Workflow '%s' completed successfully (%d steps)", wfName, len(steps))})
}

func serviceHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuthorized(r) {
		sendJSON(w, http.StatusUnauthorized, Response{Error: "Unauthorized."})
		return
	}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) != 4 || pathParts[0] != "service" {
		sendJSON(w, http.StatusNotFound, Response{Error: "Format must be /service/<type>/<name>/<action>"})
		return
	}
	svcType, svcName, action := pathParts[1], pathParts[2], pathParts[3]

	if !validNameRegex.MatchString(svcName) {
		sendJSON(w, http.StatusBadRequest, Response{Error: "Invalid service name"})
		return
	}

	if svcType == "systemctl" && !contains(appConfig.AllowedSystemctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: "Service not whitelisted"})
		return
	} else if svcType == "docker" && !contains(appConfig.AllowedDocker, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: "Container not whitelisted"})
		return
	} else if svcType == "nerdctl" && !contains(appConfig.AllowedNerdctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: "Container not whitelisted"})
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
				sendJSON(w, http.StatusNotFound, Response{Error: "Container not found"})
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
			sendJSON(w, http.StatusInternalServerError, Response{Error: out})
			return
		}
		
		expectedState := (action == "start")
		if waitErr := waitForState(svcType, svcName, expectedState); waitErr != nil {
			sendJSON(w, http.StatusInternalServerError, Response{Error: fmt.Sprintf("Command issued but verification failed: %v", waitErr)})
			return
		}
		
		sendJSON(w, http.StatusOK, Response{Status: "success", Message: fmt.Sprintf("%s %sed", svcName, action)})
		return
	}
	sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Method not allowed"})
}

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
      "info": { "title": "Remote Service Manager API", "version": "1.0.0" },
      "components": { "securitySchemes": { "bearerAuth": { "type": "http", "scheme": "bearer" } } },
      "security": [ { "bearerAuth": [] } ],
      "paths": {
        "/system/stats": { "get": { "tags": ["System"], "summary": "Get system stats", "responses": { "200": { "description": "OK" } } } },
        "/workflow/{name}": { "post": { "tags": ["Workflows"], "summary": "Trigger a predefined workflow", "parameters": [ { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } } ], "responses": { "200": { "description": "OK" } } } },
        "/service/{type}/{name}/status": { "get": { "tags": ["Services"], "summary": "Check service status", "parameters": [ { "name": "type", "in": "path", "required": true, "schema": { "type": "string", "enum": ["systemctl", "docker", "nerdctl"] } }, { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } } ], "responses": { "200": { "description": "OK" } } } },
        "/service/{type}/{name}/{action}": { "post": { "tags": ["Services"], "summary": "Start or stop a service", "parameters": [ { "name": "type", "in": "path", "required": true, "schema": { "type": "string", "enum": ["systemctl", "docker", "nerdctl"] } }, { "name": "name", "in": "path", "required": true, "schema": { "type": "string" } }, { "name": "action", "in": "path", "required": true, "schema": { "type": "string", "enum": ["start", "stop"] } } ], "responses": { "200": { "description": "OK" } } } }
      }
    };
    window.onload = () => { window.ui = SwaggerUIBundle({ spec: spec, dom_id: '#swagger-ui', deepLinking: true }); };
  </script>
</body>
</html>`
	
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime)

	loadConfig("config.json")
	
	http.HandleFunc("/service/", loggingMiddleware(serviceHandler))
	http.HandleFunc("/system/stats", loggingMiddleware(statsHandler))
	http.HandleFunc("/workflow/", loggingMiddleware(workflowHandler))
	http.HandleFunc("/docs", loggingMiddleware(docsHandler))

	portStr := fmt.Sprintf(":%d", appConfig.Port)
	log.Printf("INFO: Starting secure API at http://127.0.0.1%s", portStr)
	log.Printf("INFO: Swagger API Docs available at http://127.0.0.1%s/docs", portStr)
	
	if err := http.ListenAndServe(portStr, nil); err != nil {
		log.Fatalf("FATAL: Server failed to start: %v", err)
	}
}
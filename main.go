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
	AllowedNerdctl   []string `json:"allowed_nerdctl"` // Added nerdctl support
}

type Response struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Running *bool  `json:"is_running,omitempty"`
	Raw     string `json:"raw_status,omitempty"`
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

	providedToken := parts[1]
	return contains(appConfig.Tokens, providedToken)
}

func sendJSON(w http.ResponseWriter, statusCode int, payload Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(payload)
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
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

	// Whitelist Enforcement
	if svcType == "systemctl" && !contains(appConfig.AllowedSystemctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Service '%s' is not whitelisted for systemctl", svcName)})
		return
	} else if svcType == "docker" && !contains(appConfig.AllowedDocker, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Container '%s' is not whitelisted for docker", svcName)})
		return
	} else if svcType == "nerdctl" && !contains(appConfig.AllowedNerdctl, svcName) {
		sendJSON(w, http.StatusForbidden, Response{Error: fmt.Sprintf("Container '%s' is not whitelisted for nerdctl", svcName)})
		return
	} else if svcType != "systemctl" && svcType != "docker" && svcType != "nerdctl" {
		sendJSON(w, http.StatusBadRequest, Response{Error: "Unsupported type. Use 'systemctl', 'docker', or 'nerdctl'"})
		return
	}

	// Handle GET requests
	if r.Method == http.MethodGet {
		if action != "status" {
			sendJSON(w, http.StatusMethodNotAllowed, Response{Error: "Only /status is supported for GET"})
			return
		}

		isRunning := false
		rawStatus := ""

		if svcType == "systemctl" {
			out, err := runCommand("sudo", "systemctl", "is-active", svcName)
			isRunning = (err == nil && out == "active")
			rawStatus = out
		} else if svcType == "docker" || svcType == "nerdctl" {
			// Since nerdctl and docker share the same inspect syntax, we can use svcType directly as the command
			out, err := runCommand(svcType, "inspect", "-f", "{{.State.Running}}", svcName)
			if err != nil {
				sendJSON(w, http.StatusNotFound, Response{Error: fmt.Sprintf("Container not found or inaccessible via %s", svcType)})
				return
			}
			isRunning = (out == "true")
			rawStatus = out
		}

		sendJSON(w, http.StatusOK, Response{Running: &isRunning, Raw: rawStatus})
		return
	}

	// Handle POST requests
	if r.Method == http.MethodPost {
		if action != "start" && action != "stop" {
			sendJSON(w, http.StatusBadRequest, Response{Error: "Action must be 'start' or 'stop'"})
			return
		}

		var err error
		var out string

		if svcType == "systemctl" {
			out, err = runCommand("sudo", "systemctl", action, svcName)
		} else if svcType == "docker" || svcType == "nerdctl" {
			// Again, passing svcType ("docker" or "nerdctl") dynamically as the command
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

func main() {
	loadConfig("config.json")

	http.HandleFunc("/service/", serviceHandler)
	port := ":8000"
	fmt.Printf("Starting secure service manager at http://127.0.0.1%s\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
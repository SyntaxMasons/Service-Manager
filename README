## Features

* 🚀 **Ultra Lightweight:** Written in Go, meaning low memory footprint and no external dependencies (no Python, no Node modules).
* 🔒 **Secure by Default:** Requires a Bearer token for all requests.
* 🛡️ **Strict Whitelisting:** Services and containers must be explicitly defined in a configuration file before they can be managed.
* 🐳 **Multi-Runtime Support:** Natively supports `systemd` (via systemctl), `docker`, and `nerdctl`.
* 🛡️ **Injection Proof:** Strict regex validation on service names prevents shell injection attacks.

## Prerequisites

* [Go](https://go.dev/doc/install) 1.16 or higher (to build from source).
* `sudo` access (for `systemctl` commands).
* `docker` or `nerdctl` installed (if managing containers).

## Installation

1. **Clone the repository**
```bash
git clone https://github.com/yourusername/service-manager.git
cd service-manager

```


2. **Build the binary**
```bash
go build -o sm-api main.go

```


3. **Create the configuration file**
Create a `config.json` in the same directory as your binary to set up your tokens and whitelists.
```json
{
  "tokens": [
    "your-super-secret-token-123"
  ],
  "allowed_systemctl": [
    "nginx",
    "postgresql"
  ],
  "allowed_docker": [
    "my_webapp_container"
  ],
  "allowed_nerdctl": [
    "redis_cache"
  ]
}

```


4. **Run the server**
```bash
./sm-api

```


The API will start on `[http://127.0.0.1:8000](http://127.0.0.1:8000)`.

## API Usage

The API follows a strict URL structure:

`/{type}/{name}/{action}`

* `{type}`: Must be `systemctl`, `docker`, or `nerdctl`.
* `{name}`: The exact name of the service or container (must match the whitelist).
* `{action}`: Must be `status` (GET), `start` (POST), or `stop` (POST).

All requests require the `Authorization: Bearer <token>` header.

### 1. Check Service Status (GET)

```bash
curl -X GET \
  -H "Authorization: Bearer your-super-secret-token-123" \
  http://localhost:8000/service/systemctl/nginx/status

```

**Response:**

```json
{
  "is_running": true,
  "raw_status": "active"
}

```

### 2. Start a Service (POST)

```bash
curl -X POST \
  -H "Authorization: Bearer your-super-secret-token-123" \
  http://localhost:8000/service/docker/my_webapp_container/start

```

**Response:**

```json
{
  "status": "success",
  "message": "my_webapp_container started successfully"
}

```

### 3. Stop a Service (POST)

```bash
curl -X POST \
  -H "Authorization: Bearer your-super-secret-token-123" \
  http://localhost:8000/service/nerdctl/redis_cache/stop

```

## Running in Production

To keep the API running in the background persistently, it is highly recommended to run it as a systemd service.

1. Create a service file: `sudo nano /etc/systemd/system/service-manager.service`
2. Add the following configuration (update the paths to match your system):

```ini
[Unit]
Description=Service Manager API
After=network.target

[Service]
Type=simple
User=your_linux_user
WorkingDirectory=/path/to/your/project
ExecStart=/path/to/your/project/sm-api
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target

```

3. Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable service-manager
sudo systemctl start service-manager

```

> **Note on systemctl permissions:** The API executes `sudo systemctl` commands. The user running this API (e.g., `your_linux_user`) must have passwordless `sudo` privileges configured in `/etc/sudoers` for the `systemctl` binary to work without prompting for a password.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

# AWS Infrastructure Helper

AWS Infrastructure Helper is a Go-based tool designed to simplify and automate common AWS infrastructure tasks. It provides a set of utilities and APIs to help manage, configure, and interact with AWS resources efficiently.

## Features
- Modular architecture for easy extension
- API endpoints for infrastructure operations
- Configuration management via YAML files
- Logging and error handling utilities
- Docker support for containerized deployment

## Project Structure
```
├── conf/                  # Configuration files (YAML)
├── internal/
│   ├── core/              # Core utilities and services
│   └── infrastructure/    # AWS infrastructure APIs and config
├── Dockerfile             # Docker container setup
├── docker-compose.yml     # Multi-container orchestration
├── main.go                # Application entry point
├── go.mod, go.sum         # Go module dependencies
```

## Getting Started
### Prerequisites
- Go 1.18+
- Docker (optional, for containerized usage)
- AWS credentials configured (via environment or AWS CLI)

### Build and Run
```bash
# Build the project
make build

# Run the application
make run
```
Or use Docker:
```bash
docker-compose up --build
```

## Configuration
Edit `conf/application.yml` to set up your AWS credentials, regions, and other settings.

## Usage
- The main application logic is in `main.go`.
- Core utilities are under `internal/core/common`.
- Infrastructure APIs are under `internal/infrastructure/api`.

## How It Works

The application runs two background goroutines that periodically perform checks and update AWS resources:

1. **DNS Updater** – Every `DNSUpdater.CheckInterval` it:
   * Fetches the current public IP from the configured URL. If the fetch fails or the response is not a valid IPv4 address, the check is skipped.
   * Resolves the DNS record for the target domain.
   * If the IP has changed (or the record cannot be resolved), it updates the Route 53 A record for each hosted zone.

2. **ISP Fallback Updater** – Every `ISPFallbackUpdater.CheckInterval` it:
   * Calls the port‑fetcher URL to determine if the ISP port is closed. Any HTTP response counts as open.
   * The fallback is only enabled after `failure-threshold` consecutive connection failures (default 3), or after `dns-failure-threshold` consecutive failures of any kind (default 30). DNS resolution failures only count toward the second threshold, so a short local DNS outage does not trigger a failover.
   * If the port state changes, it toggles the DNS CNAME record between the normal and fallback values in every hosted zone.
   * When switching to fallback it also:
     * Sets the CloudFront distribution origin to the fallback origin.
     * Increases the Auto‑Scaling Group desired capacity to 1 and cancels any pending shutdown.
   * When switching back to normal it:
     * Schedules the Auto‑Scaling Group to shut down after the configured `ShutdownTime`.
     * Reverts the CloudFront origin to the normal origin.
   * A failed AWS call is retried on the next check; it never stops the background loop.

All AWS interactions use the AWS SDK v2 and are authenticated with the credentials defined in `conf/application.yml`.

## Configuration

The YAML file contains three main sections:

| Section | Purpose |
|---------|---------|
| `dns-updater` | Settings for the DNS updater (interval, public‑IP fetcher, record details). |
| `isp-fallback-updater` | Settings for the ISP fallback logic (interval, port fetcher, DNS record, CloudFront, Auto‑Scaling Group). |
| `aws` | AWS credentials and region. |

Example values are provided in the repository.

## Extending

* To add a new periodic task, create a method on `HelperService` and call it from `main.go`.
* To change the fetcher logic, modify `FetcherApi` or replace the URL in the config.

## Running Locally

```bash
# Build
go build -o aws-helper

# Run
./aws-helper
```

Or with Docker:

```bash
docker-compose up --build
```

The container will load the configuration from `conf/application.yml` and start the two goroutines.

## License

MIT License – see the `LICENSE` file.

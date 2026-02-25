# Simple Web Host

Static file hosting service for Raspberry Pi with security-first design.

## Features

- **Security hardened**: Non-root container, read-only filesystem, no new privileges
- **Path traversal protection**: Flat file structure only, no subdirectories
- **Extension whitelist**: Only approved file types served (`.log` explicitly blocked)
- **Directory access blocked**: All directory requests return 403
- **Rolling logs**: Hourly log files in Central Time with 7-day retention

## Quick Start

### 1. Host Setup (Raspberry Pi)

Create directories and set ownership:

```bash
sudo mkdir -p /mnt/data/static/www /mnt/data/static/logs
sudo chown -R 65534:65534 /mnt/data/static
```

### 2. Deploy

```bash
docker compose up -d --build
```

### 3. Connect Cloudflared

```bash
docker network connect static cloudflared
```

## Adding Files

Copy files to `/mnt/data/static/www/` on the Pi. Only whitelisted extensions are served:

| Allowed | Blocked |
|---------|---------|
| .html | .log (reserved) |
| .css | |
| .js | |
| .png, .jpg, .gif, .svg, .webp | |
| .json, .txt, .md | |

## Logs

Logs are stored in `/mnt/data/static/logs/` with hourly rotation:
- Format: `YYYY-MM-DDTHH.log`
- Timezone: America/Chicago
- Retention: 7 days (168 hours)

View current logs:
```bash
cat /mnt/data/static/logs/$(date +%Y-%m-%dT%H).log
```

## Security Details

- Container runs as `nobody` (UID 65534)
- Read-only root filesystem
- All capabilities dropped
- No shell in container (distroless image)
- No directory traversal possible
- Subdirectory access blocked

## Network

The `static` network is created for this service. Your manually-managed `cloudflared` container connects to it.

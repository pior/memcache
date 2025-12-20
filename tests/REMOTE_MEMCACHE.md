# Running Memcache Nodes on a Remote Server

This guide explains how to run memcache nodes on a remote server (using Podman or Docker) and connect to them from your local machine for testing.

## Quick Start

### 1. Start Memcache on Remote Server

```bash
# On the remote server (using podman-compose)
cd ~/memcache
podman-compose -f docker-compose-memcache.yml up -d
```

This starts 3 memcache containers on ports 11211-11213.

### 2. Configure and Run Loadgen

```bash
# Set MEMCACHE_HOST to the remote server's hostname
# The hostname will be automatically resolved to an IP address
export MEMCACHE_HOST=misaki

# Start local infrastructure
cd tests
docker compose up -d toxiproxy prometheus grafana

# Run loadgen - it will connect directly to the remote server
go run ./cmd/loadgen -concurrency 50 -hot-keys 100
```

That's it! No SSH tunnels or special networking needed.

## How It Works

The connection flow is:

```
Loadgen → Toxiproxy (localhost:21211-21213)
    ↓
Toxiproxy → 10.0.0.234:11211-11213 (resolved from "misaki")
    ↓
Memcache containers on remote server
```

When `MEMCACHE_HOST` is set, the code automatically:
1. Resolves the hostname to an IP address (e.g., `misaki` → `10.0.0.234`)
2. Configures toxiproxy upstreams to use the IP address directly
3. Docker containers can reach the remote IP directly (no special networking needed)

## Architecture

```
┌─────────────────────────────────────┐
│  Remote Server (misaki @ 10.0.0.234)│
│                                     │
│  ┌──────────┐ ┌──────────┐ ┌─────┐│
│  │memcache1 │ │memcache2 │ │mc3  ││
│  │:11211    │ │:11212    │ │:11213││
│  └──────────┘ └──────────┘ └─────┘│
└─────────────────────────────────────┘
          ▲
          │ Direct TCP connection
          │ (Docker bridge can reach LAN IPs)
          │
┌─────────┴─────────────────────────────┐
│  Local Machine                        │
│                                       │
│  ┌─────────────────────────────────┐ │
│  │  Toxiproxy (Docker)             │ │
│  │  → 10.0.0.234:11211-11213       │ │
│  │     (proxied as :21211-21213)   │ │
│  └─────────────────────────────────┘ │
│                                       │
│  ┌──────────┐     ┌──────────┐      │
│  │Prometheus│     │ Grafana  │      │
│  └──────────┘     └──────────┘      │
└───────────────────────────────────────┘
```

## Remote Server Setup (Podman)

### Step 1: Copy Compose File

```bash
scp docker-compose-memcache.yml user@remote-host:~/memcache/
```

### Step 2: Start Memcache Containers

```bash
ssh user@remote-host
cd ~/memcache
podman-compose -f docker-compose-memcache.yml up -d
```

### Step 3: Verify Containers

```bash
podman ps
# Should show:
# memcache_node1 on port 11211
# memcache_node2 on port 11212
# memcache_node3 on port 11213
```

## Local Machine Setup

### Configure Environment

```bash
cd tests
export MEMCACHE_HOST=misaki
docker compose up -d toxiproxy prometheus grafana
```

## Troubleshooting

### Test Direct Connectivity

**From local machine to remote server:**
```bash
nc -zv misaki 11211
nc -zv misaki 11212
nc -zv misaki 11213
```

**From Docker container to remote server:**
```bash
docker run -ti --rm ubuntu bash -c "apt update; apt install -y iputils-ping netcat-openbsd; nc -zv 10.0.0.234 11211"
```

### Toxiproxy Can't Connect

**Verify MEMCACHE_HOST is set:**
```bash
echo $MEMCACHE_HOST
# Should output: misaki (or any non-empty value)
```

**Check toxiproxy configuration:**
```bash
curl -s http://localhost:8474/proxies | jq -r '.[] | "\(.name): \(.upstream)"'
# Should show: 10.0.0.234:11211-11213 (or whatever IP your hostname resolves to)
```

**Test connection through toxiproxy:**
```bash
echo "stats" | nc localhost 21211
```

### Circuit Breakers Opening

This usually means the connection is failing. Check:

1. Remote memcache is running: `ssh misaki 'podman ps'`
2. Network connectivity: `nc -zv misaki 11211`
3. Toxiproxy upstreams are correct: `curl http://localhost:8474/proxies | jq`
4. MEMCACHE_HOST is set and resolves: `echo $MEMCACHE_HOST` and `nslookup $MEMCACHE_HOST`
5. Docker can reach the IP: `docker run --rm busybox ping -c 3 10.0.0.234`

## Managing Remote Memcache

### Stop Containers
```bash
ssh misaki 'cd ~/memcache && podman-compose down'
```

### Restart Containers
```bash
ssh misaki 'cd ~/memcache && podman-compose restart'
```

### View Logs
```bash
ssh misaki 'podman logs memcache_node1'
```

### Clear All Data
```bash
ssh misaki 'podman exec memcache_node1 sh -c "echo flush_all | nc localhost 11211"'
```

## Performance Considerations

Running memcache on a remote server introduces network latency:

- **Local (Docker)**: ~0.1-0.5ms latency
- **Remote (LAN)**: ~1-5ms latency (depends on network)
- **Remote (VPN)**: ~10-100ms latency

This is useful for:
- Testing with realistic network conditions
- Simulating geographically distributed deployments
- Separating test load from development machine

Keep in mind:
- Circuit breaker timeouts may need adjustment
- Baseline performance will be lower
- Latency scenarios will compound with network latency

# Forward Proxy (TCP HTTP/HTTPS)

A lightweight TCP forward proxy written in Go.
Supports HTTP & HTTPS inspection via SNI and Host header parsing, automatic multi-port listening, and a loop-safe port-offset mechanism that allows running inside Docker without NAT feedback loops.

---

## 🚀 **How it Works**

This proxy accepts raw TCP connections, detects whether the traffic is:

* HTTP (via request line + Host header)
* HTTPS (via TLS ClientHello + SNI)

Then it routes the connection to the **correct upstream server** based on the extracted hostname.

### ⚠️ Why Port Offset Is Necessary

When running inside Docker, exposing the same port you forward to (e.g., `443`) causes the proxy to **send its own outbound traffic back into itself**, creating infinite loops.

Example of the loop:

```
client → proxy:443 → proxy forwards to :443 → docker NAT rewrites → proxy:443 again → ...
```

To fix this, the proxy uses **port offsets**:

| Public Port | Proxy Container Port | Backend Port |
| ----------- | -------------------- | ------------ |
| 443         | 9443                 | 443          |
| 80          | 9080                 | 80           |

Meaning:

```
client:443 → host:443 → container:9443 → proxy → backend:443 (real internet)
```

This prevents loops completely because the proxy **never listens on the same port it forwards to**.

### 🧮 How Port Offset Works Internally

When a connection arrives on a port such as:

```
9443  → backend 443
9080  → backend 80
9xxx  → backend (xxx)
```

Port mapping rule:

```
backendPort = incomingPort - 9000
```

This logic is built into the proxy via `calculateBackendPort()`.

---

## 🛠 **Usage**

Run the proxy binary:

```bash
$ ./forward_proxy -h
```

Output:

```
Usage of ./forward_proxy:
  -help
        Display help message
  -host string
        Host to listen on (default "0.0.0.0")
  -log-level string
        Logging level: [debug, info, warn, error] (default "info")
  -port string
        Port(s) to listen to.
        Supports:
          - single port: 9443
          - multiple ports: 9443,9080,9555
          - ranges: 9000-9999
        IMPORTANT:
          When running inside Docker, use offset ports like 9443 → 443.
        (default "8080")
  -protocol string
        Only "tcp" is supported (default "tcp")
```

---

## 🐳 **Docker Usage**

Pull the image:

```bash
docker pull ghcr.io/mrmohebi/froward-proxy:latest
```

Run in loop-safe mode:

```bash
docker run -p 443:9443 ghcr.io/mrmohebi/froward-proxy \
    -port 9443 \
    -log-level debug
```

Or for HTTP + HTTPS:

```bash
docker run \
  -p 443:9443 \
  -p 80:9080 \
  ghcr.io/mrmohebi/froward-proxy \
  -port 9443,9080 \
  -log-level info
```

Now the proxy listens on:

```
9443 → backend:443
9080 → backend:80
```

---

## 📦 **Features**

### ✔ HTTP over TCP

Reads HTTP request line + Host header.

### ✔ HTTPS over TCP

Parses TLS ClientHello → extracts SNI.

### ✔ Multiple ports

Example:

```
-port 9443,9080,9555
```

### ✔ Port ranges

Example:

```
-port 9000-9050
```

### ✔ Loop-safe inside Docker

Thanks to offset mapping (9443→443).

### ❌ UDP (planned)

UDP modes for HTTP/HTTPS are listed below.

---

## 📋 **To-Do**

* [x] HTTP over TCP
* [x] HTTPS over TCP
* [x] Multiple port listeners
* [x] Loop-safe port offset mode
* [ ] HTTP over UDP (QUIC/HTTP3)
* [ ] HTTPS over UDP (DTLS/QUIC)

---

# 🎯 **Summary**

This forward proxy:

* Handles both HTTP & HTTPS
* Extracts hostname correctly
* Forwards traffic based on SNI/Host
* Supports multiple ports + ranges
* Runs safely inside Docker using port offsets
* Prevents infinite TCP loops
* Requires zero configuration besides exposing offset ports
* Lightweight, fast, zero dependencies
